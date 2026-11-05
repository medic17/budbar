// Command budbar reads or switches Dolby Audio on Shokz OpenFit 2+ earbuds
// over Bluetooth RFCOMM from Windows, and its tray also shows the listening
// mode of Bose QC Ultra earbuds and headphones.
//
//	budbar [-trace] [-device name] status|on|off
//	budbar devices | add name | remove name
//	budbar tray|install|uninstall
//
// It talks only to devices added with add (or the tray's Add device menu),
// and only while they are connected to this PC. devices lists the added
// devices and the paired ones of a tested model that can be added; add and
// remove take part of a device's name, or its address, and change
// devices.json in %LOCALAPPDATA%\budbar. status, on and off run once on the
// added Shokz earbuds (-device picks one if several are added) and print the
// result. tray shows the state as a tray icon, switches from its menu or
// Ctrl+Q, and logs every action with -trace detail to
// %LOCALAPPDATA%\budbar\budbar.log. install copies the exe there and starts
// the tray at every logon; uninstall removes that task.
//
// The exe's manifest (budbar.manifest, embedded via the generated .syso)
// marks it detached: it uses the console of the terminal that starts it, and
// gets no console window when the logon task or Explorer starts it.
//
// Release build, then install from the built exe:
//
//	go build -trimpath -ldflags "-s -w" -o budbar.exe ./cmd/budbar
//	.\budbar.exe install
//
// -s -w drop the symbol table and debug info; -trimpath keeps local source
// paths out of the exe. Go optimizes every build, so these change size, not
// speed.
package main

//go:generate go run github.com/akavel/rsrc@v0.10.2 -manifest budbar.manifest -o rsrc_windows_amd64.syso

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"openfit-dolby/bt"
	"openfit-dolby/devices"
	"openfit-dolby/shokz"
)

// dialFunc opens an RFCOMM stream to the earbuds at a Bluetooth address.
type dialFunc func(addr uint64) (io.ReadWriteCloser, error)

// Commands that do not talk to the earbuds themselves, the device list and
// the connection check status, on and off pick from; tests replace them.
var (
	store        = devices.Default()
	connected    = bt.Connected
	startTray    = runTray
	runInstall   = install
	runUninstall = uninstall
)

func main() {
	os.Exit(run(os.Args[1:], dialRFCOMM, os.Stdout, os.Stderr))
}

// run checks all arguments before dialing, so bad input never opens a
// connection. It returns the process exit code.
func run(args []string, dial dialFunc, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("budbar", flag.ContinueOnError)
	fs.SetOutput(stderr)
	trace := fs.Bool("trace", false, "log every byte sent and received to stderr")
	device := fs.String("device", "", "which added Shokz earbuds status, on and off use: part of the name, or the address")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: budbar [-trace] [-device name] status|on|off\n       budbar devices | add name | remove name\n       budbar tray|install|uninstall")
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "The tray always logs with -trace detail, to "+logPath())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	cmd, rest := fs.Arg(0), fs.Args()[1:]
	usage := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "budbar: "+format+"\n", args...)
		fs.Usage()
		return 2
	}
	switch cmd {
	case "status", "on", "off":
	case "devices", "add", "remove", "tray", "install", "uninstall":
		if *device != "" {
			return usage("-device applies to status, on and off")
		}
	default:
		return usage("unknown command %q", cmd)
	}
	switch cmd {
	case "add", "remove":
		if len(rest) == 0 {
			return usage("%s needs part of a device's name, or its address", cmd)
		}
	default:
		if len(rest) != 0 {
			return usage("%s takes no arguments", cmd)
		}
	}
	switch cmd {
	case "tray":
		return startTray(dial, stderr)
	case "install":
		return runInstall(stdout, stderr)
	case "uninstall":
		return runUninstall(stdout, stderr)
	case "devices":
		return listDevices(stdout, stderr)
	case "add":
		return addDevice(strings.Join(rest, " "), stdout, stderr)
	case "remove":
		return removeDevice(strings.Join(rest, " "), stdout, stderr)
	}

	list, err := store.Load()
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	dev, err := devices.Pick(list, devices.Shokz, *device, connected)
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	var tr *bt.Tracer
	if *trace {
		tr = bt.NewTracer(stderr)
		tr.Logf("command %q, connecting to %v at %012X", cmd, dev, dev.Addr)
	}
	conn, err := dial(dev.Addr)
	if err != nil {
		if tr != nil {
			tr.Logf("connect failed: %v", err)
		}
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	if tr != nil {
		tr.Logf("connected")
		tr.RW = conn
		conn = tr
	}
	defer conn.Close()
	c := shokz.NewClient(conn)

	var on bool
	if cmd == "status" {
		on, err = c.Status()
	} else {
		on = cmd == "on"
		_, err = c.Set(on)
	}
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	fmt.Fprintln(stdout, map[bool]string{true: "on", false: "off"}[on])
	return 0
}
