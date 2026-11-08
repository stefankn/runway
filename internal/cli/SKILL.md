---
name: runway
description: Build, run and read the logs of an iOS, iPadOS or macOS app in an Xcode project with the runway CLI, on a simulator or a physical device. Use when asked to build the app, run or launch it, check its console output or logs, list simulators or devices, or confirm that a change works in the running app.
---

# runway

`runway` builds and runs Xcode apps from the terminal, like Xcode's toolbar. It wraps `xcodebuild`, `xcrun simctl` and `xcrun devicectl`, and finds the workspace or project and the scheme by itself.

## Before you start

- Run runway from the folder with the `.xcworkspace`/`.xcodeproj`, or below a folder with a `.runway.toml`.
- `runway config --json` shows the scheme, project, destination and other settings runway will use, and where each comes from. If it can't settle on a scheme, pass `--scheme <name>` or ask the user to run `runway init`.
- Always pass a destination (`--sim` or `--device`). Without one, runway may need to ask, and outside a terminal it fails with a list of candidates instead.

## Build

```sh
runway build --sim --quiet
```

`--quiet` shows only errors and the result. On success, runway prints the app path and bundle ID. On failure, it exits non-zero with Xcode's error, followed by a `hint:` line for known problems (signing, Developer Mode, missing destination). Read the hint, but fix the cause Xcode reports.

## Run

`runway run` builds, installs and launches the app, then streams its console (`print`, stdout, stderr) **until the app exits or is stopped. Most apps never exit, so always use `--timeout`**:

```sh
runway run --sim --headless --timeout 30s
runway run --sim --headless --timeout 30s --logs     # also Logger / os_log messages
runway run --sim --headless --timeout 30s --no-build # relaunch the last build
runway run --sim --headless --timeout 30s --env API_URL=http://localhost:8080 -- -FeatureFlag YES
```

- `--timeout` starts counting when the app launches, so the build doesn't count. When it expires, runway stops the app and exits 0. If the app exits or crashes first, runway exits with the app's status.
- Always pass `--headless`, so the simulator window doesn't jump in front of the user's work.
- Running again replaces the copy that is already running.
- Lines starting with `log │` come from the unified log (`--logs`). Everything else is the app's console.
- Arguments after `--` go to the app.

## Logs

`runway logs` without `--last` streams forever. Use `--last` to read recent messages and return:

```sh
runway logs --last 2m                       # the app's subsystem, booted simulator
runway logs --last 2m --level info --category network
runway logs --last 2m --json                # NDJSON, one message per line
runway logs --last 2m --all                 # everything, not just the app
```

On a physical device, recent logs need `sudo log collect`. runway prints the command for the user to run instead. For live device logs, use `runway run --device --logs --timeout 30s`.

## Simulators and devices

```sh
runway simulators --json            # also: --booted, --platform ios, --runtime 27
runway devices --json               # connected, paired devices
```

Pass a name or UDID to `--sim` / `--device` (`--sim "iPhone 18 Pro"`). Names match exactly, then ignoring case, then as a unique prefix. If runway prints a list of candidates instead of running, pick one and pass its UDID. Don't guess a different name.

## Troubleshooting

- Add `-v` to print every underlying `xcodebuild` / `simctl` / `devicectl` command, ready to run by hand.
- Exit status: 0 on success, the failing tool's or the app's status otherwise, 130 after an interrupt.
- Devices need to be paired, unlocked and in Developer Mode, and builds for devices need a team (`--team` or `team` in `.runway.toml`). Ask the user to fix these. You can't from the terminal.
