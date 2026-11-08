# Xcode 27.2 notes

Behaviour of Apple's tools that runway relies on, verified on macOS 27.2 with
Xcode 27.2 (build 27B52). When Apple changes something, update this file along
with the code in `internal/xcode`.

## devicectl JSON (version 5)

Checked 2026-11-07 with devicectl 651.32. The full field mapping, including
the deprecated fallbacks, is documented on `deviceFromJSON` in
[`internal/xcode/devicectl.go`](../internal/xcode/devicectl.go). Key points:

- The UDID that `xcodebuild -destination id=…` needs is
  `properties.hardware.udid`. The top-level `identifier` is a CoreDevice UUID.
- devicectl also lists **simulators** (`hardware.reality == "simulated"`).
  Physical devices often omit `reality`, so runway filters on "not simulated".
- Connection: `transportType` is `"wired"` (USB) or `"localNetwork"`;
  `state` / `tunnelState` is `"connected"` while a tunnel is up,
  `"disconnected"` when reachable but idle, and `"unavailable"` when out of reach.
- `developerModeStatus` is `{"enabled": {"mode": 1}}` in `properties` and the
  string `"enabled"` in the deprecated `deviceProperties`. `osVersionNumber` is
  an object with `stringValue` in `properties` and a plain string in the old field.
- Device names can contain a non-breaking space (U+00A0), e.g. "Apple Watch".

## devicectl stdout

`devicectl list devices --json-output -` writes only JSON to stdout. Its
human-readable table goes to stderr.

## Xcode 27.2 JSON project format

The new format is a `project.xcproj` JSON file **inside** the `.xcodeproj`
bundle, replacing `project.pbxproj`. It is not a standalone `Foo.xcproj` next
to the bundle. runway therefore detects `*.xcodeproj` bundles for both formats.

- [x] `xcodebuild -list -json -project X.xcodeproj` works for a bundle that
  contains only `project.xcproj` (confirmed 2026-11-07), so `-project` needs no
  special handling for the new format.

## xcodebuild -list -json

Output goes to stdout as `{"project": {name, schemes, targets, configurations}}`
or `{"workspace": {name, schemes}}`. Every `.xcodeproj` contains a
`project.xcworkspace`, so detection only looks at the top level of a folder.

## xcodebuild -showBuildSettings -json

A list of `{action, target, buildSettings}`. runway picks the entry with
`WRAPPER_EXTENSION = app` (preferring the target named after the scheme) and
uses `TARGET_BUILD_DIR/WRAPPER_NAME` and `PRODUCT_BUNDLE_IDENTIFIER`. With
`-derivedDataPath` and `-destination` given, `TARGET_BUILD_DIR` is
`<derived data>/Build/Products/<Configuration>-<sdk>`. For a scheme whose app
embeds a widget extension, only the app target was listed.

## Destinations by UDID

Confirmed 2026-11-07: UDIDs printed by `runway simulators` and `runway devices`
work as `-destination platform=…,id=<UDID>`, both for a simulator and for a
wired iPad (a signed device build succeeded).

## xcodebuild errors (for build hints)

Confirmed with Xcode 27.2:

- Bad destination, exit status 70:
  `xcodebuild: error: Unable to find a device matching the provided destination specifier:`
  followed by the destinations it considered and why each doesn't fit.
- Compile errors exit with status 65.

Still unconfirmed (the hint patterns are best guesses): the exact signing,
locked-device, Developer Mode and pairing messages. Check them with a real
device build and update `internal/cli/hints.go`.

## SIGINT during `simctl launch --console-pty`

Checked 2026-11-07: simctl passes SIGINT on to the app, which quits at once;
simctl then exits with status **0**. A following
`simctl terminate <udid> <bundle id>` prints `found nothing to terminate` and
also exits 0. So after Ctrl-C, runway's explicit terminate (like Xcode's Stop
button) is a harmless safety net, and nothing is killed twice. Because simctl
exits 0 either way, runway decides "interrupted" from its own context, not from
simctl's exit status.

Devices, checked 2026-11-07: `devicectl … --console` forwards SIGINT to the
app, which quits ("App terminated due to signal 2."). devicectl prints a
warning if it gets a *second* SIGINT after the app has gone ("Failed to
forward Interrupt: 2 to remote process"). That used to happen because a
terminal Ctrl-C reached devicectl directly *and* through runway. runway now
starts every child in its own process group, so only runway receives the
terminal's signals and it sends exactly one SIGINT to each child's group.
runway also handles SIGHUP the same way, so closing the terminal window still
stops the app.

Known limitation on devices: when the app exits by itself, devicectl reports
"The app terminated with the exit code 0." and exits 0 even when the app
called `exit(3)` (seen with a Debug build). runway can only pass on what
devicectl returns. Simulators report the real exit status.

## Simulator window

Xcode 27.2 has no Simulator.app. Simulators are shown in **DeviceHub**
(`Xcode.app/Contents/Applications/DeviceHub.app`, bundle ID
`com.apple.dt.Devices`). runway opens it with `open -b com.apple.dt.Devices`,
by bundle ID so a rename doesn't break it.

Known limitation: this brings DeviceHub to the front but doesn't select the
simulator runway booted. `open "devices://device/open?id=<UDID>"` (the URL
form devicectl reports for physical devices) opens DeviceHub without selecting
a simulator either. Left as is; revisit if Apple documents a way.

DeviceHub's device list comes from CoreDevice (the same as `devicectl list
devices`), not straight from `simctl`. After old Xcode versions it can show
stale simulators that `simctl` no longer has. Deleting unavailable simulators
(`xcrun simctl delete unavailable`) and restarting the Mac brought the two
lists back in sync.

## Simulator boot

`simctl bootstatus <udid> -b` boots the simulator if needed and waits until it
has finished booting; it returns at once for a booted simulator. runway uses it
instead of `simctl boot`, so `install` never runs on a half-booted simulator.

## Running on a device (devicectl 651.32)

- Install: `devicectl device install app --device <udid> <path.app>`.
- Launch: `devicectl device process launch --console --terminate-existing
  --device <udid> <bundle id> [-- args…]`. With `--console`, devicectl waits
  for the app to exit, forwards catchable signals to it, and connects its
  standard streams; `--json-output -` can't be combined with `--console`.
- Environment variables: set `DEVICECTL_CHILD_<KEY>` in devicectl's own
  environment (the same pattern as simctl's `SIMCTL_CHILD_`).
- Terminate takes a PID: `devicectl device process terminate --device <udid>
  --pid <pid>`. runway finds the PID with `devicectl device info processes
  --device <udid> --json-output -` (`result.runningProcesses[]` with
  `executable` as a `file://` URL and `processIdentifier`). An installed app's
  executable is `/private/var/containers/Bundle/Application/<uuid>/<App>.app/<Executable>`;
  its extensions run from `…/<App>.app/PlugIns/<X>.appex/<X>`, so matching
  the `/<App>.app/<Executable>` ending can't pick an extension.
- `devicectl` can't install from inside another sandbox: it fails with
  `sandbox_extension_issue_file … Operation not permitted`. This only affects
  sandboxed development environments, not normal use.

## Simulator log stream (`simctl spawn … log stream`)

Checked 2026-11-08 on an iOS 27.2 simulator:

- `xcrun simctl spawn <udid> log stream --style ndjson --level debug` writes
  only NDJSON to stdout. Besides `logEvent` lines there are other event types
  (e.g. `activityCreateEvent`), which runway skips. stderr carries noise such
  as `getpwuid_r did not find a match for uid 501`.
- **Stopping it is the hard part.** `simctl spawn` ignores SIGINT and SIGTERM,
  and even `kill -9` on simctl leaves the `log` process inside the simulator
  running. That process is started by `launchd_sim`, so it is not in runway's
  process group. It only dies by itself when its stdout is closed *and* it next
  writes; with a narrow predicate that write may never come.
- What works: SIGINT to the in-simulator `log` process's PID, either from the
  host (`kill -INT <pid>`) or through `xcrun simctl spawn <udid> /bin/kill -INT
  <pid>`. Plain `kill` is not on the simulator's PATH (ENOENT); use `/bin/kill`.
  Simulator processes are visible to host `pgrep`.
- So runway (`LogStream.Stop` in `internal/xcode/logstream.go`) adds a unique
  term to the predicate (`eventMessage != "runway-<random>"`), finds the
  in-simulator process with `pgrep -f '^log stream .*runway-<random>'` (the
  `^log stream` excludes simctl, whose command line contains the marker
  too), sends it SIGINT with `kill -INT <pid>`, and only then stops simctl.
- With a predicate, `log` also prints `Filtering the log data using "…"`. The
  fixture `testdata/log/stream.ndjson` starts with that line so the parser is
  tested against it. All its other lines are real output, captured from
  system processes in the simulator.
- `log show` has no `--level`: it takes `--info` and `--debug` flags instead.
- When interrupted, `log stream --style ndjson` prints a final summary line,
  `{"count":30,"finished":1}`, which is not a log event.
- Verified 2026-11-08 outside the sandbox with the sample app: `runway logs`
  and `runway run --sim --logs` stop their `log` process on Ctrl-C and when
  the app exits by itself (`pgrep -fl "^log stream"` finds nothing afterwards).

## Device logs

See [device-logs.md](device-logs.md): devicectl can't stream device logs.
runway uses `OS_ACTIVITY_DT_MODE=1` for `run --device --logs` (verified on an
iPad) and `log collect` + `log show` for `logs --device --last` (`log collect`
needs root, so runway prints the `sudo` command and `logs --archive`).
