# Unified logs from physical devices

Can a physical device's unified log (`Logger`, `os_log`) be streamed from the
command line with Xcode 27.2? This file records what was found and what
runway does.

## Findings

Checked 2026-11-08 with Xcode 27.2 (devicectl 651.32):

- **No live stream through Apple's tools.** `log stream` on the Mac only reads
  the Mac's own log, and devicectl has no log or syslog subcommand. The
  `device` subcommands are appResize, capture, copy, info, install,
  notification, orientation, pairings, pasteboard, process, profile, reboot,
  rename, settings, simulate, sysdiagnose and uninstall.
- **`log collect --device-udid <udid> --last <time> --output <x>.logarchive`**
  copies recent messages from a device into an archive, and `log show --style
  ndjson … <archive>` prints them. Confirmed 2026-11-08: for an attached
  device it **needs root**. Without it, it fails with `log: Must be root to
  collect logs from attached device` and exit status 77.
- **`OS_ACTIVITY_DT_MODE=1`** in an app's environment makes it copy its
  `Logger` / `os_log` messages to stderr. Xcode sets this variable itself
  when it runs an app. Confirmed 2026-11-08: through `devicectl … launch
  --console` (as `DEVICECTL_CHILD_OS_ACTIVITY_DT_MODE=1`), the messages show
  up live in the console of an iPad. On a simulator (`SIMCTL_CHILD_…`) they
  appear as `2026-11-08 11:31:40.605201+0100 ActorTest[82995:911541] [tick]
  notice tick 1`. **Debug messages were not included** on the simulator,
  only info and above.

## What runway does

| Command | Device behaviour |
|---|---|
| `runway run --device` | Shows stdout and stderr (`print()`), as before. |
| `runway run --device --logs` | Also sets `OS_ACTIVITY_DT_MODE=1` for the app, so `Logger` messages (info and above) show up live in its console, in the format above rather than with a `log │` prefix. |
| `runway logs --device` | Fails with a message pointing to `--last` and `run --device --logs`. |
| `runway logs --device --last 5m` | Tries `log collect` into a temporary `.logarchive`, then `log show` on it. Because `log collect` needs root, this normally fails; runway then prints the `sudo log collect … --output device.logarchive` command to run, followed by `runway logs --archive device.logarchive`. runway doesn't run sudo itself, so a password prompt never appears in the middle of a command. |
| `runway logs --archive <file>` | Shows a `.logarchive` with the usual filters and formatting. No device or simulator is needed. |

If neither approach works on a given setup, Console.app (select the device in
the sidebar) is the fallback for live device logs.

Not checked yet: whether `OS_ACTIVITY_DT_MODE` includes debug messages on a
device. It didn't on the simulator.
