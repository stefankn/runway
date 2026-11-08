# runway — Implementation Plan (v0.1)

A Go command-line tool that does from the terminal what Xcode's toolbar does: build, run with live console output, stream logs, and list devices and simulators. It is installed with Homebrew.

> Note: another package named `runway` already exists in Homebrew. Ours ships from a personal tap (`brew install <you>/tap/runway`), so the formulas don't conflict; just avoid installing both binaries on one machine.

---

## 1. Context

- **Target environment:** macOS on Apple silicon with **Xcode 27.2** (Xcode 27 only runs on Apple silicon and needs macOS Tahoe 26.6 or later). Physical devices must run iOS 17 or later, which is what Xcode 27 supports for on-device work.
- **What it wraps:** `xcodebuild`, `xcrun simctl` and `xcrun devicectl`. It is a thin, friendly layer over Apple's tools and does not reimplement them.
- **Prior art:** a working bash script (`run.sh`) already does build → install → launch → console. Use it as the reference behaviour. It is included at the bottom of this file.

## 2. Scope

### In scope for v0.1
| Command | Purpose |
|---|---|
| `runway devices` | List connected and paired physical devices |
| `runway simulators` | List available simulators |
| `runway build` | Build the app for a simulator or device |
| `runway run` | Build, install and launch the app, then stream its console until Ctrl-C |
| `runway logs` | Stream unified logging (`Logger` / `os_log`) output for the app |
| `runway version` | Print the version, commit and build date |

### Out of scope for now
Tests, archiving and TestFlight uploads, simulator management (create, erase, boot as its own command), `doctor`, attaching a debugger. Section 11 lists these as future work. Design the code so they are easy to add.

## 3. Design principles (please follow these strictly)

1. **Keep all Apple-tool wrapping in one small layer** (`internal/xcode`). No other package may call `exec` or build `xcrun` or `xcodebuild` arguments. When Apple changes a flag or a JSON field, the fix should touch one place.
2. **Parse JSON, never human-readable text.** Every list or query has a JSON mode. Use it:
   - `xcodebuild -list -json`
   - `xcodebuild -showBuildSettings -json`
   - `xcrun simctl list devices available -j` (also `runtimes -j`)
   - `xcrun devicectl list devices --json-output -`. As of Xcode 27, `--json-output` can write to stdout, so no temp file is needed.
   - `log stream --style ndjson` (or `json`) for logs
3. **Handle devicectl's JSON migration.** Xcode 27 (JSON version 5) deprecated `hardwareProperties`, `deviceProperties` and `connectionProperties` in favour of a single `properties` dictionary, and adds a `_deprecationNotice` field. Read `properties` first and fall back to the old fields. Keep this logic in one function, covered by fixtures of real output.
4. **Never go through a shell.** Use `exec.Command(name, args...)` with argument slices, and do no string concatenation into `sh -c`. Paths and simulator names contain spaces.
5. **Stream, don't buffer.** Build output, console output and logs pipe straight to the user's terminal as they arrive.
6. **Be transparent.** `--verbose` / `-v` prints every underlying command, already quoted, before it runs, so the user can copy and paste it. This is the main debugging aid when something breaks.
7. **Exit codes propagate.** If `xcodebuild` fails, `runway` exits non-zero. If the app exits during `run`, `runway` exits with a matching status where possible.
8. **Clean Ctrl-C.** On SIGINT/SIGTERM during `run` or `logs`: stop the child processes, terminate the launched app (as Xcode's Stop button does), then exit. Never leave orphaned `log stream` processes.
9. **Keep dependencies few.** Allowed: `spf13/cobra` (CLI), a TOML library (`BurntSushi/toml` or `pelletier/go-toml/v2`), and optionally `charmbracelet/huh` (or similar) for the interactive picker. Ask before adding anything else.
10. **Make it testable without Xcode.** All exec calls go through a `Runner` interface, so unit tests use fake runners and recorded JSON fixtures.

## 4. Architecture

```
runway/
├── cmd/runway/main.go              # entry point; sets version via ldflags
├── internal/
│   ├── cli/                      # cobra commands: root, devices, simulators, build, run, logs, version
│   ├── xcode/                    # ★ the ONLY place that touches Apple tools
│   │   ├── runner.go             # Runner interface + real exec implementation (+ verbose printing)
│   │   ├── xcodebuild.go         # List(), ShowBuildSettings(), Build()
│   │   ├── simctl.go             # ListSimulators(), Boot(), Install(), Launch(), Terminate(), LogStream()
│   │   ├── devicectl.go          # ListDevices(), Install(), Launch(), Terminate()
│   │   └── types.go              # clean domain types: Device, Simulator, BuildSettings, Scheme…
│   ├── project/                  # find .xcworkspace / .xcodeproj / .xcproj, pick the scheme, resolve app path + bundle ID
│   ├── destination/              # turn flags/config/picker input into a concrete Simulator or Device
│   ├── config/                   # load .runway.toml, merge with env + flags
│   └── ui/                       # colours, spinner/status lines, table output, picker, friendly errors
├── testdata/                     # real JSON captured from Xcode 27.2 (see §9)
├── .goreleaser.yaml
└── .github/workflows/release.yml
```

**Domain types** (in `internal/xcode/types.go`) are what the rest of the app uses. They are small, stable Go structs, and Apple's raw JSON never leaks out of `internal/xcode`. Sketch:

```go
type Simulator struct {
    UDID, Name, Runtime, State string // State: "Booted" | "Shutdown"
    OSVersion                  string
    Platform                   string // iOS, watchOS, tvOS, visionOS
}

type Device struct {
    UDID, Name, Model, OSVersion string
    Platform                     string
    Connection                   string // "wired" | "network" | "unavailable"
    Paired, DeveloperMode        bool
}
```

## 5. Configuration

Settings are resolved in this order (highest priority first): **command-line flags → environment variables (`RUNWAY_*`) → `.runway.toml` → auto-detection.**

`.runway.toml` sits in the project root. `runway` searches upward from the current directory to find it.

```toml
scheme        = "MyApp"
workspace     = "MyApp.xcworkspace"   # or: project = "MyApp.xcodeproj"
configuration = "Debug"
team          = "ABCDE12345"          # DEVELOPMENT_TEAM, optional
destination   = "sim:iPhone 17 Pro"   # or "device:<UDID>" or "device" (first connected)
derived_data  = ".build/runway"         # default
log_subsystem = "com.example.MyApp"   # for `runway logs`; defaults to the bundle ID
```

**Auto-detection** (when not configured):
- Workspace beats project. If there are several, error and list them, unless stdin is a TTY, in which case show a picker.
- Scheme: from `xcodebuild -list -json`. If there's exactly one, use it. If there are several, show a picker when interactive; otherwise error and list them.
- Optional for v0.1: `runway init` writes a `.runway.toml` from the detected values.

## 6. Destination selection (shared by build, run, logs)

Flags:
- `--sim [name|UDID]`: a simulator. If the value is empty, use the configured default, or else the first available iPhone simulator on the newest iOS runtime.
- `--device [name|UDID]`: a physical device. If the value is empty, use the first connected device (wired preferred over network).
- `--destination <raw>`: an escape hatch, passed straight to `xcodebuild -destination`.

Rules:
- Name matching is exact first, then case-insensitive, then unique-prefix. If a name is ambiguous (for example, the same simulator name on two runtimes), show a picker when interactive, or else error with the candidates and their UDIDs.
- If nothing is specified and nothing is configured: show a picker of devices and simulators when interactive; otherwise use the default simulator.
- `xcodebuild` destinations: `platform=iOS Simulator,id=<UDID>` and `platform=iOS,id=<UDID>`. Use the UDID, never the name. In Xcode 27, `devicectl list devices` shows the UDID, which `xcodebuild` accepts.
- Supporting non-iOS platforms (watchOS, tvOS, visionOS) is nice to have. Pick the platform string from the simulator's or device's platform rather than hard-coding iOS.

## 7. Command specifications

### `runway devices`
- Runs `devicectl list devices --json-output -` and parses it as described in principle 3.
- Table columns: Name, Model, OS, Connection, UDID. Paired, connected devices come first.
- Flags: `--json` (prints runway's own clean `[]Device` JSON, not Apple's), `--all` (include unavailable or unpaired devices).
- Accepted when it shows the same devices as Device Hub, and the UDIDs it prints work in `xcodebuild -destination`.

### `runway simulators`
- Runs `simctl list devices available -j`.
- Table columns: Name, Runtime, State, UDID. Sorted by platform, then newest runtime, then name. Booted simulators are highlighted.
- Flags: `--json`, `--booted`, `--platform ios|watchos|tvos|visionos`, `--runtime <version>`.

### `runway build`
- Resolves the project, scheme, configuration and destination, then runs:
  `xcodebuild build <-workspace|-project> … -scheme … -configuration … -destination … -derivedDataPath <dd> -allowProvisioningUpdates [DEVELOPMENT_TEAM=…]`
- **Output:** if `xcbeautify` is on PATH, pipe through it (and say so in `--verbose`). Otherwise show raw output. `--raw` forces raw output, `--quiet` shows only errors and the final status.
- On success, print the app path and bundle ID (from `-showBuildSettings -json`: pick the target with `WRAPPER_EXTENSION == "app"`, then use `TARGET_BUILD_DIR/WRAPPER_NAME` and `PRODUCT_BUNDLE_IDENTIFIER`).
- Pass-through: everything after `--` goes to `xcodebuild` unchanged.
- **Friendly errors** (hint printed after the raw error, never replacing it):
  - signing or "requires a development team" → suggest `team` in config or `--team`
  - device locked, Developer Mode off, or not paired → say how to fix it (Device Hub → Pair, Settings → Privacy & Security → Developer Mode)
  - "Unable to find a destination" → suggest `runway simulators` / `runway devices`

### `runway run`
Equivalent to Xcode's Run button:
1. Build (skip with `--no-build`).
2. **Simulator:** boot it if needed (`simctl boot`, tolerating "already booted"), open the Simulator / Device Hub window unless `--headless`, `simctl install`, then
   `simctl launch --console-pty --terminate-running-process <UDID> <bundleID> [-- app args]`
3. **Device:** `devicectl device install app --device <UDID> <app>`, then
   `devicectl device process launch --console --terminate-existing --device <UDID> <bundleID> [-- app args]`
4. Stream stdout and stderr to the terminal until the app exits or the user presses Ctrl-C.
5. **On Ctrl-C:** terminate the app explicitly (`simctl terminate`, or `devicectl device process terminate`). **Verify** what the `--console` modes already do on SIGINT, and don't double-kill.

Flags: `--no-build`, `--headless`, `--logs` (also stream `Logger` output in the same terminal, interleaved and with a dimmed prefix; simulator first, device if feasible, see §8), `--env KEY=VAL` (repeatable; passed as `SIMCTL_CHILD_*` for simulators, and through devicectl's environment option for devices — check `--help`), and app launch arguments after `--`.

Accepted when:
- `runway run --sim` behaves like pressing ⌘R in Xcode on a simulator: `print()` output appears live, and Ctrl-C stops the app.
- The same works with `--device` on a wired iPhone and on a network-paired one.
- Running it twice in a row replaces the running app instead of failing.

### `runway logs`
Streams unified logging for the app, filtered by default to `subsystem == "<log_subsystem or bundleID>"`.
- **Simulator:** `xcrun simctl spawn <UDID> log stream --style ndjson --level debug --predicate '<predicate>'`. Parse each line and print it formatted: time, level (coloured), category, message.
- Flags: `--level default|info|debug`, `--category <c>`, `--predicate <raw>` (overrides the filter), `--process <name>`, `--all` (no filter), `--json` (pass NDJSON through as-is).
- **Physical device: research spike first.** `log stream` on the Mac does not stream from iOS devices. Check whether Xcode 27's `devicectl` (or another supported CLI) can stream device logs. If not, v0.1 handles devices by:
  - `runway run --device` already showing stdout and stderr, and
  - `runway logs --device --last 5m`, which uses `log collect --device-udid <UDID> --last 5m --output <tmp>.logarchive` followed by `log show --style ndjson --predicate … <archive>` (check whether it needs sudo, and handle that gracefully), and
  - if neither works, clearly saying so and suggesting Console.app.
  Write down what you find in `docs/device-logs.md`.

## 8. Things to verify on a real Mac before or while implementing

Don't guess about these. Check each against Xcode 27.2 and record the result in a comment or in `docs/`:

> Results so far are in [`docs/xcode-notes.md`](docs/xcode-notes.md) (items 1, 2, 3, 5 and 6 checked; item 4 in [`docs/device-logs.md`](docs/device-logs.md)).
1. The exact devicectl JSON (version 5): the keys inside `properties` that give name, UDID, OS version, model, platform, connection or tunnel state, pairing state and Developer Mode.
2. Whether `devicectl … --json-output -` mixes any non-JSON text into stdout.
3. What `simctl launch --console-pty` and `devicectl … launch --console` do on SIGINT: does the app keep running?
4. Whether there is any CLI way to stream `os_log` from a physical device (see §7, logs).
5. Whether `-project` accepts the new JSON-based `.xcproj` project format added in Xcode 27.2, and how auto-detection should treat it.
6. Whether a simulator window should be opened with `open -a "Device Hub"` or `open -a Simulator` in Xcode 27.

## 9. Testing

- **Fixtures:** capture real output from Xcode 27.2 into `testdata/`: `xcodebuild -list -json` (both project and workspace), `-showBuildSettings -json` (an app with a test target and an extension), `simctl list -j`, `devicectl list devices` JSON with a wired device, a network device and an unavailable device, and a few `log stream` NDJSON lines. Remove personal data such as device names, UDIDs and team IDs.
- **Unit tests** (no Xcode needed): JSON parsing, including both new and old devicectl formats; destination resolution and ambiguity handling; config precedence; argument construction for every wrapped command (asserting exact argument slices through a fake `Runner`); log line formatting.
- **Integration tests:** behind a `//go:build integration` tag. They run only on a Mac with Xcode. Build and run a tiny sample app (`testdata/SampleApp`, a SwiftUI app that prints and logs once per second) on a simulator, and assert that output arrives.
- **Manual smoke checklist** in `docs/smoke-test.md` covering each acceptance criterion above.
- CI: GitHub Actions on `macos-latest`. Run `go vet`, `staticcheck`, the unit tests, and the integration tests where Xcode 27 is available.

## 10. Distribution (Homebrew)

- **GoReleaser:** build `darwin/arm64` only (Xcode 27 is Apple-silicon-only, so an Intel build would be pointless). Use `-trimpath` and inject version, commit and date with `-ldflags`.
- **Homebrew tap:** a separate repo `github.com/stefankn/homebrew-tap`. GoReleaser's `homebrew_casks:` section updates `Casks/runway.rb` on each release. (Its `brews:` formulas for prebuilt binaries are deprecated.) Users install with `brew install stefankn/tap/runway`. GoReleaser makes the cask depend on macOS and `arch: :arm64`.
- **Release workflow:** `just release X.Y.Z` checks the repo and pushes a `vX.Y.Z` tag. That triggers GitHub Actions, which runs the tests and GoReleaser and updates the tap. This needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret.
- Shell completions: generated with cobra (`runway completion zsh|bash|fish`). The cask generates them at install time.
- Signing: GoReleaser signs the binary with a Developer ID certificate and notarizes it in the release workflow (the `MACOS_SIGN_*` / `MACOS_NOTARY_*` secrets). This is required: Homebrew quarantines cask downloads, and the cask runs the binary during the install to generate completions.

## 11. Milestones (build in this order; each one ends in a working, tested state)

1. **Scaffold:** Go module, cobra root, `version`, the `Runner` interface with `--verbose` command echo, CI with lint and tests.
2. **`simulators` + `devices`:** parsers, fixtures, table output, and `--json`.
3. **Project detection + config:** workspace/project/scheme detection, `.runway.toml`, the precedence rules, and the picker.
4. **`build`:** destination resolution, xcbeautify passthrough, friendly errors, printing the app path and bundle ID.
5. **`run` on a simulator:** boot, install, launch with console, clean Ctrl-C.
6. **`run` on a device:** install, launch with console, terminate. Test both wired and network-paired devices.
7. **`logs`:** simulator streaming, the device research spike and its fallback, `run --logs`.
8. **Release:** GoReleaser, the Homebrew tap, completions, README with install and usage instructions.

## 12. Future ideas (not v0.1; keep the design open to them)

`runway test` (with a readable failure summary), `runway archive` / `runway upload` (TestFlight via an App Store Connect API key: `-authenticationKeyPath/-ID/-IssuerID`), `runway simulators boot|erase|create`, `runway doctor` (checks the Xcode version, `xcode-select` path, pairing and Developer Mode), `runway init`, a debugger flag (`run --debug`, launching with `--start-stopped` and attaching lldb), watch mode (rebuild and rerun when files change).

---

## Appendix: reference bash script (`run.sh`)

This is the behaviour v0.1 `run` should match or improve on. Note that its device auto-detection reads the **deprecated** `hardwareProperties` field. The Go version should use `properties`, falling back to the old fields as described in §3.

```bash
#!/usr/bin/env bash
set -euo pipefail
TARGET="${1:-sim}"; WHICH="${2:-}"; CONFIG="${CONFIG:-Debug}"; DERIVED="build"

if ls *.xcworkspace >/dev/null 2>&1; then
  CONTAINER=(-workspace "$(ls -d *.xcworkspace | head -1)"); KEY=workspace
else
  CONTAINER=(-project "$(ls -d *.xcodeproj *.xcproj 2>/dev/null | head -1)"); KEY=project
fi
SCHEME="${SCHEME:-$(xcodebuild -list -json "${CONTAINER[@]}" | jq -r ".${KEY}.schemes[0]")}"

if [[ "$TARGET" == "sim" ]]; then
  # resolve UDID by name / UDID / first iPhone via: xcrun simctl list devices available -j
  DEST="platform=iOS Simulator,id=$UDID"
  xcrun simctl boot "$UDID" 2>/dev/null || true
else
  # resolve UDID via: xcrun devicectl list devices --json-output <file>
  DEST="platform=iOS,id=$UDID"
fi

xcodebuild build "${CONTAINER[@]}" -scheme "$SCHEME" -configuration "$CONFIG" \
  -destination "$DEST" -derivedDataPath "$DERIVED" -allowProvisioningUpdates -quiet

# APP + BUNDLE_ID from: xcodebuild -showBuildSettings -json … | select(.WRAPPER_EXTENSION=="app")

if [[ "$TARGET" == "sim" ]]; then
  xcrun simctl install "$UDID" "$APP"
  exec xcrun simctl launch --console-pty --terminate-running-process "$UDID" "$BUNDLE_ID"
else
  xcrun devicectl device install app --device "$UDID" "$APP"
  exec xcrun devicectl device process launch --console --terminate-existing --device "$UDID" "$BUNDLE_ID"
fi
```
