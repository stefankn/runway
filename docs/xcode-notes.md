# Xcode 27.2 notes

Results of the checks in PLAN.md §8, verified on macOS 27.2 with Xcode 27.2
(build 27B52). When Apple changes something, update this file along with the
code in `internal/xcode`.

## §8.1 devicectl JSON (version 5)

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

## §8.2 devicectl stdout

`devicectl list devices --json-output -` writes only JSON to stdout. Its
human-readable table goes to stderr.

## §8.5 Xcode 27.2 JSON project format

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

## Destinations by UDID (PLAN §7 acceptance)

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

## Still to check

- §8.3 what `simctl launch --console-pty` / `devicectl … --console` do on SIGINT (Milestone 5/6)
- §8.4 streaming `os_log` from a physical device (Milestone 7)
- §8.6 `open -a "Device Hub"` vs `open -a Simulator` (Milestone 5)
