<p align="center">
  <img src="docs/logo.svg" alt="runway logo" width="160">
</p>

# runway

Build and run Xcode apps from the terminal, the way Xcode's toolbar does.

`runway` is a thin layer over `xcodebuild`, `xcrun simctl` and `xcrun devicectl`. It finds your project and scheme, picks a simulator or device, builds, installs and launches the app, and streams its console output until you press Ctrl-C.

```sh
runway run --sim              # like ⌘R on a simulator
runway run --device           # same, on the first connected iPhone or iPad
```

## Requirements

- A Mac with Apple silicon
- Xcode 27.2 or later
- Physical devices running iOS 17 or later, paired with Developer Mode turned on
- Optional: [xcbeautify](https://github.com/cpisciotta/xcbeautify), which `runway` uses to format build output when it's installed

## Installation

With Homebrew:

```sh
brew install stefankn/tap/runway
```

This also installs shell completions for zsh, bash and fish. Update with `brew upgrade runway`.

Homebrew has an unrelated package that is also called `runway`, so always use the full name `stefankn/tap/runway` to install, and don't install both.

From source, with Go 1.27 or later:

```sh
go install github.com/stefankn/runway/cmd/runway@latest
```

Or clone the repository and run `just build`, which writes the binary to `bin/runway`.

## Quick start

From the folder that holds your `.xcworkspace` or `.xcodeproj`:

```sh
runway init        # save the detected workspace/project and scheme to .runway.toml
runway simulators  # see which simulators are available
runway run --sim "iPhone 18 Pro"
```

## Commands

| Command | What it does |
|---|---|
| `runway run` | Build, install and launch the app, then stream its console until it exits or you press Ctrl-C |
| `runway logs` | Stream the app's unified log (`Logger`, `os_log`) from a simulator |
| `runway build` | Build the app for a simulator or device |
| `runway simulators` | List available simulators |
| `runway devices` | List connected and paired physical devices |
| `runway init` | Write a `.runway.toml` for the project in this folder |
| `runway config` | Show the settings runway would use and where each one comes from |
| `runway version` | Print the version, commit and build date |
| `runway completion` | Generate shell completions (zsh, bash, fish, powershell) |

Run `runway <command> --help` to see all flags and examples.

### Choosing a destination

`build`, `run` and `logs` accept:

- `--sim [name|UDID]`: a simulator. With no value, runway uses the configured default, or else an iPhone on the newest iOS runtime.
- `--device [name|UDID]`: a physical device. With no value, runway uses the first connected device, preferring a wired connection over the network.
- `--destination <value>`: passed straight to `xcodebuild -destination`.

Names match exactly first, then ignoring case, then as a unique prefix. If a name matches more than one simulator or device, or you don't specify a destination at all, runway shows a picker when it runs in a terminal. Otherwise it lists the candidates and their UDIDs.

### run

```sh
runway run --sim
runway run --sim "iPhone 18 Pro" --env API_URL=http://localhost:8080
runway run --device "iPhone 13"
runway run --no-build -- -FeatureFlag YES     # arguments after -- go to the app
runway run --sim --logs                       # also show Logger / os_log messages
```

The app's `print()`, stdout and stderr appear live. Ctrl-C stops the app, just like Xcode's Stop button. Running again replaces the copy that is already running. Use `--headless` to keep the simulator window in the background.

`Logger` and `os_log` messages go to the unified log, not to stdout, so `run` doesn't show them by default. Add `--logs` to see them in the same terminal, each prefixed with `log │`. On a device, `--logs` sets `OS_ACTIVITY_DT_MODE=1` for the app instead, which makes it copy those messages (info and above) to its console (see [docs/device-logs.md](docs/device-logs.md)).

### logs

```sh
runway logs                              # the booted simulator, filtered to the app's subsystem
runway logs --level info --category network
runway logs --all                        # everything, with process names
runway logs --predicate 'messageType == error'
runway logs --last 10m                   # recent messages instead of a live stream
runway logs --device --last 5m           # devices can only show recent messages
runway logs --archive device.logarchive  # a log archive collected earlier
```

By default `logs` shows messages whose subsystem is `log_subsystem` from the configuration, or else the app's bundle ID. Without `--sim` or `--device` it uses the booted simulator when exactly one is booted. `--json` prints the log tool's NDJSON unchanged.

Physical devices can't stream their log to the command line. For live messages from a device, use `runway run --device --logs`. For recent ones, `logs --device --last 5m` uses `log collect`, which needs root for a device: runway prints the `sudo log collect` command to run, and you can then read the result with `runway logs --archive`. Details are in [docs/device-logs.md](docs/device-logs.md).

### build

```sh
runway build --sim
runway build --device --team ABCDE12345
runway build -- -showBuildTimingSummary       # arguments after -- go to xcodebuild
```

When the build succeeds, runway prints the app path and bundle ID. Use `--raw` to skip xcbeautify, or `--quiet` to see only errors and the result. For common failures, such as signing, Developer Mode or a missing destination, runway prints a hint after Xcode's own error.

### simulators and devices

```sh
runway simulators --booted
runway simulators --platform ios --runtime 27
runway devices --all       # include unpaired and unavailable devices
```

Both commands accept `--json`, which prints runway's own JSON instead of a table. The UDIDs they print work with `--sim`, `--device` and `xcodebuild -destination`.

## Configuration

Settings are resolved in this order, highest priority first:

1. Command-line flags
2. `RUNWAY_*` environment variables, e.g. `RUNWAY_SCHEME`
3. `.runway.toml` in the current folder or the nearest parent folder
4. Auto-detection (workspace or project, scheme)
5. Built-in defaults

Example `.runway.toml`:

```toml
scheme        = "MyApp"
workspace     = "MyApp.xcworkspace"   # or: project = "MyApp.xcodeproj"
configuration = "Debug"
team          = "ABCDE12345"          # DEVELOPMENT_TEAM
destination   = "sim:iPhone 18 Pro"   # or "device:<name or UDID>", or "device" for the first connected one
derived_data  = ".build/runway"       # the default; relative to the file
log_subsystem = "com.example.MyApp"   # for logs and run --logs; defaults to the bundle ID
```

Run `runway config` to see the resolved value of each setting and where it came from. If you use the default derived data folder, add `.build/` to your `.gitignore`.

## Troubleshooting

Add `-v` / `--verbose` to any command to print every underlying `xcodebuild`, `simctl` or `devicectl` command before it runs. The commands are quoted, so you can copy one and run it yourself.

runway exits with the status of the failed tool, or of the app when it stops during `run`. After Ctrl-C it exits with 130.

## Development

```sh
just build    # build bin/runway
just test     # unit tests (no Xcode needed)
just ci       # everything CI checks: tidy, formatting, vet, staticcheck, tests
```

To release, check the packaging with `just release-check` (a local GoReleaser dry run into `dist/`), and then run `just release 0.2.0`. After its checks and a confirmation, it pushes the `v0.2.0` tag. GitHub Actions then publishes the release and updates the Homebrew cask in [stefankn/homebrew-tap](https://github.com/stefankn/homebrew-tap).

`docs/xcode-notes.md` records what has been verified about Xcode 27.2's tools.

## License

MIT. See [LICENSE](LICENSE).
