# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`runway` is a Go CLI that does from the terminal what Xcode's toolbar does: build, run with live console output, stream logs, and list devices/simulators. It is a thin wrapper over `xcodebuild`, `xcrun simctl`, `xcrun devicectl` and `log`, targeting Xcode 27.2 on Apple silicon. `README.md` documents the commands and flags. `docs/xcode-notes.md` records verified behaviour of Apple's tools; update it alongside `internal/xcode` when that behaviour changes or new behaviour gets verified.

## Commands

Tasks live in the `justfile` (`just` lists them):

- `just build`: build `bin/runway` with version ldflags
- `just run <args>`: e.g. `just run -v simulators`
- `just test`: `go test -race ./...`. Single test: `just test -run TestName ./internal/cli/` or `go test -race -run TestName ./internal/cli/`
- `just lint`: `go vet` + staticcheck (pinned version, run via `go run`)
- `just fmt` / `just fmt-check`
- `just ci`: everything CI runs (`go mod tidy -diff`, fmt-check, lint, test). Run this before declaring work done.
- `just release-check`: `goreleaser check` plus a snapshot build into `dist/` (publishes nothing)
- `just release X.Y.Z`: tags and pushes `vX.Y.Z` after checks (clean `main` equal to `origin/main`, new tag, `just ci`) and a confirmation prompt. Only the user runs this.

Unit tests need no Xcode.

## Releasing

A pushed `v*` tag runs `.github/workflows/release.yml`: unit tests, then GoReleaser (`.goreleaser.yaml`). It builds `darwin/arm64` only, publishes a GitHub release, and updates the cask `Casks/runway.rb` in `stefankn/homebrew-tap` using the `HOMEBREW_TAP_GITHUB_TOKEN` secret. GoReleaser signs the binary with a Developer ID certificate and notarizes it when the `MACOS_SIGN_*` / `MACOS_NOTARY_*` secrets are set, and skips that step locally. The cask generates shell completions by running `runway completion` during the install, so a release must be notarized: Gatekeeper kills a quarantined binary that isn't.

## Architecture

`cmd/runway/main.go` → `cli.Execute` (cobra). Packages under `internal/`:

- **`xcode`**: the *only* package that runs processes or knows Apple-tool arguments. Everything goes through the `Runner` interface (`runner.go`): `ExecRunner` in production, `FakeRunner` (`fakerunner.go`) in tests. `Client` methods build exact argument slices and parse Apple's JSON into small domain types (`types.go`). Apple's raw JSON never leaves this package.
- **`config`**: `.runway.toml` (found by searching upward from the cwd), merged with precedence flags → `RUNWAY_*` env → file → auto-detection. `Resolved` tracks the source of each value (shown by `runway config`).
- **`project`**: detects workspace/project (workspace wins; only `*.xcodeproj` bundles, which also covers the Xcode 27.2 JSON `project.xcproj` format) and the scheme.
- **`destination`**: turns `--sim` / `--device` / `--destination` / config / the picker into a concrete simulator or device. Name matching goes exact → case-insensitive → unique prefix, and ambiguous matches go to the picker or an error listing UDIDs.
- **`lines`**: an `io.Writer` that splits streamed output into lines (used to parse and format `log` output).
- **`ui`**: colours, tables, the `huh`-based picker. The picker is nil when stdin/stderr is not a TTY, and callers must then fail with a list of candidates instead of prompting.
- **`cli`**: cobra commands. A shared `options` struct carries `--verbose` plus test hooks (`testRunner`, `testPicker`, `workDir`). CLI tests call `execute(...)` or `runOpts(...)` with a `FakeRunner` and assert on the recorded commands. `hints.go` scans tool output for known failures (signing, Developer Mode, missing destination) and prints a hint *after* the raw error, never in place of it.

### Process and signal model

Read this before touching `run`, `Runner`, or signal handling:
- Every child runs in its own process group (`Setpgid`). On context cancel, `ExecRunner` sends one SIGINT to the group, then kills after `WaitDelay` (3s).
- `cli.Execute` turns the first SIGINT/SIGTERM/SIGHUP into context cancellation and then restores default handling, so a second Ctrl-C exits immediately. Interrupt exits with status 130.
- Child exit codes propagate through `ExitCoder` (`*xcode.ExitError`, and `appExitError` in `run.go`).
- `run` stops the launched app explicitly on Ctrl-C (`simctl terminate` / `devicectl … process terminate`), as Xcode's Stop button does.
- `log stream` runs inside the simulator via `simctl spawn`, which ignores signals, and the in-simulator `log` survives killing `simctl`. `LogStream` (`logstream.go`) therefore starts it on a detached context with a marker in the predicate (`eventMessage != "runway-<random>"`), finds its PID with `pgrep -f` (simulator processes are host processes), and stops it with `kill -INT`, which makes it exit cleanly. See `docs/xcode-notes.md`.

## Rules

- Never call `exec` or build `xcrun`/`xcodebuild` args outside `internal/xcode`. Never go through a shell. Use argument slices, because paths and simulator names contain spaces.
- Parse JSON output modes only, never human-readable text. devicectl JSON v5: read `properties` first and fall back to the deprecated `hardwareProperties`/`deviceProperties`/`connectionProperties`, all inside `deviceFromJSON` (`devicectl.go`). The `xcodebuild` UDID is `properties.hardware.udid`, not the top-level `identifier`.
- Pass destinations to `xcodebuild` by UDID, never by name.
- Stream output; don't buffer it. `--verbose` echoes every command shell-quoted (`Cmd.String`), ready to paste.
- Allowed dependencies: cobra, BurntSushi/toml, charmbracelet/huh. Ask before adding anything else.
- Parser tests use real Xcode 27.2 output in `internal/xcode/testdata/<tool>/`, with personal data scrubbed. Command tests assert exact argument slices through `FakeRunner`.
