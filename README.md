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

## Adding your devices

Pair the device with Windows first. Then add it, either from the tray's
right-click menu (**Add device**) or on the command line:

```powershell
budbar devices          # what is added, and what can be added
budbar add openfit      # any part of the name, or the address
budbar add bose earbuds
budbar remove headphones
```

Only paired devices of a tested model (listed above) can be added; others
never show up. The list is kept in `%LOCALAPPDATA%\budbar\devices.json`.
Before every connection the tools check that the device is in that list
and that Windows' own records still identify it as that model, so a
hand-edited file can't point them at another device. A running tray picks
up changes the next time you open its menu or a device connects.

## Usage

```
budbar [-trace] [-device name] status|on|off
budbar devices | add name | remove name
budbar tray|install|uninstall
```

| Command         | What it does                                          |
| --------------- | ----------------------------------------------------- |
| `status`        | Print whether Dolby Audio is on or off, then exit     |
| `on`, `off`     | Switch Dolby Audio, then exit                         |
| `devices`       | List added devices and the ones you can add           |
| `add`, `remove` | Add or remove a device by part of its name or address |
| `tray`          | Run the tray icon                                     |
| `install`       | Copy the exe and start the tray at every logon        |
| `uninstall`     | Remove the logon task                                 |

`-trace` logs every byte sent and received. `-device` picks which Shokz
earbuds to use when you have added more than one.

There is also a Bose-only command:

```
bose [-device name] [-trace] [-for duration] mode|watch
```

`mode` prints the active listening mode. `watch` prints every packet the
device sends for `-for` (default 2 minutes). Without `-device` it uses your
only Bose device, or of several the one connected now.

## Limits

- Windows only. The devices must be paired and connected to this PC.
- Only the models listed above have been tested, so only they can be added.
- The Shokz protocol is undocumented. It was worked out from a Bluetooth
  capture, and the tool only replays the on/off frames it saw there.
- Unsigned builds that register a logon task can trip Windows Defender's
  heuristics (a false positive seen once on this project).

## Development

```powershell
go test ./...
```

Packages: `shokz` and `bose` hold each protocol, `bt` the Windows Bluetooth
layer (RFCOMM and paired-device records), `devices` the tested models and
the device list, and `cmd/` the two executables.

## Disclaimer

Not affiliated with Shokz or Bose. Use at your own risk.
