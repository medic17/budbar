# budbar

A small Windows tray tool for Bluetooth earbuds and headphones. It talks to
the device directly over Bluetooth RFCOMM, so you don't need the phone app.

- **Shokz OpenFit 2+**: show and switch Dolby Audio on or off.
- **Bose QC Ultra Earbuds / Headphones (2nd Gen)**: show the active listening
  mode (read only).

The tray icon shows the current state, the menu switches Dolby Audio, and
`Ctrl+Q` toggles it. Everything is logged to
`%LOCALAPPDATA%\budbar\budbar.log`.

## Install

Needs Windows 10/11 and [Go](https://go.dev/dl/).

```powershell
go build -trimpath -ldflags "-s -w" -o budbar.exe ./cmd/budbar
.\budbar.exe install
```

`install` copies the exe to `%LOCALAPPDATA%\budbar` and registers a scheduled
task that starts the tray at every logon. `budbar uninstall` removes the task.

## Usage

```
budbar [-trace] status|on|off|tray|install|uninstall
```

| Command     | What it does                                        |
| ----------- | --------------------------------------------------- |
| `status`    | Print whether Dolby Audio is on or off, then exit   |
| `on`, `off` | Switch Dolby Audio, then exit                       |
| `tray`      | Run the tray icon                                   |
| `install`   | Copy the exe and start the tray at every logon      |
| `uninstall` | Remove the logon task                               |

`-trace` logs every byte sent and received.

There is also a Bose-only command:

```
bose [-device earbuds|headphones] [-trace] [-for duration] mode|watch
```

`mode` prints the active listening mode. `watch` prints every packet the
device sends for `-for` (default 2 minutes).

## Limits

- Windows only. The devices must already be paired and connected to this PC.
- The Bluetooth addresses are hard-coded: `earbudsAddr` in
  [cmd/budbar/main.go](cmd/budbar/main.go) and `bose.Devices` in
  [bose/bose.go](bose/bose.go). Change them to your own devices' addresses
  before building. Only the devices listed above have been tested.
- The Shokz protocol is undocumented. It was worked out from a Bluetooth
  capture, and the tool only replays the on/off frames it saw there.
- Unsigned builds that register a logon task can trip Windows Defender's
  heuristics (a false positive seen once on this project).

## Development

```powershell
go test ./...
```

Packages: `shokz` and `bose` hold each protocol, `bt` the Windows RFCOMM
layer, and `cmd/` the two executables.

## Disclaimer

Not affiliated with Shokz or Bose. Use at your own risk.
