# Smoke test

A manual check of PLAN.md's acceptance criteria on a real Mac, to run before
tagging a release. The unit tests cover parsing and command construction; this
covers the real tools, devices and signals.

## Setup

- Xcode 27.2 or later, outside any sandbox. In a sandbox, building SwiftUI
  apps, `log` and installing on devices fail.
- A sample app that prints and logs once per second. The checks below use
  `ActorTest` (bundle ID `com.github.stefankn.ActorTest`), which prints
  `tick N` and logs every level in `Logger` category `tick`.
- A wired iPhone or iPad with Developer Mode on. For the network check, the
  same device or another one paired over the network.

```sh
cd ~/Developer/runway && just build
R=$PWD/bin/runway
cd ~/Developer/ActorTest
```

After every check that ends with Ctrl-C, nothing of runway's may be left
running:

```sh
pgrep -fl "^log stream"           # no output
pgrep -fl "simctl (launch|spawn)"  # no output
```

## Listing

- [ ] `$R simulators` lists the same simulators as Device Hub, booted ones highlighted.
- [ ] `$R simulators --booted --json` prints JSON with only booted simulators.
- [ ] `$R devices` lists the same devices as Device Hub. `--all` adds unavailable ones.
- [ ] A UDID from `$R devices` works with `xcodebuild -destination "platform=iOS,id=<UDID>"`.

## Build

- [ ] `$R build --sim` succeeds and prints the app path and bundle ID.
- [ ] `$R build --device` succeeds (signing works).
- [ ] `$R build --sim "No Such Phone"` fails with a message that lists how to find destinations.
- [ ] `$R -v build --sim` prints the `xcodebuild` command, and pasting it into a shell works.

## Run on a simulator

- [ ] `$R run --sim`: `tick N` lines appear live. Ctrl-C stops the app, and runway exits with 130.
- [ ] Run it twice in a row: the second run replaces the first app instead of failing.
- [ ] `$R run --sim -- -exit3` exits with status 3 (`echo $?`).
- [ ] `$R run --sim --env FOO=bar` prints `FOO = bar`.
- [ ] `$R run --sim --headless` doesn't bring Device Hub to the front.

## Run on a device

- [ ] `$R run --device` on a **wired** device: `tick N` appears live, and Ctrl-C stops the app on the device.
- [ ] The same over the **network** (unplug the cable and wait until `$R devices` shows `network`).
- [ ] Running twice in a row replaces the app.

## Logs

- [ ] With the app running on a simulator, `$R logs` streams `debug`/`info`/`default`/`error`/`fault tick N`, with coloured levels.
- [ ] `$R logs --level info` shows no debug lines. `$R logs --category printer` shows a line when you tap "Send print".
- [ ] `$R logs --json | jq -c .` prints valid JSON.
- [ ] `$R logs --last 2m` prints recent messages and exits.
- [ ] `$R run --sim --logs` shows `tick N` and `log │ …` lines together.
- [ ] `$R run --sim --logs -- -exit3` exits with 3 and prints no warning.
- [ ] `$R run --device --logs` shows the app's `Logger` messages (info and above) in its console.
- [ ] `$R logs --device --last 5m` prints the `sudo log collect` hint. After running that command, `$R logs --archive device.logarchive` shows the app's messages.
- [ ] After every Ctrl-C above, the `pgrep` checks print nothing.

## Release packaging

- [ ] `just release-check` passes, and `dist/runway_darwin_arm64_*/runway version` prints the snapshot version.
- [ ] After the release: `brew install stefankn/tap/runway`, `runway version` prints the new version, and `runway <Tab>` completes in zsh.
