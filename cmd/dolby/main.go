// Command dolby reads or switches Dolby Audio on Shokz OpenFit 2+ earbuds
// over Bluetooth RFCOMM from Windows.
//
//	dolby [-trace] status|on|off|tray|install|uninstall
//
// It talks only to the earbuds at earbudsAddr, and only while they are
// connected to this PC. status, on and off run once and print the result. tray shows the state as
// a tray icon, switches from its menu or Ctrl+Q, and logs every action with
// -trace detail to %LOCALAPPDATA%\openfit-dolby\dolby.log. install copies the
// exe there and starts the tray at every logon; uninstall removes that task.
//
// The exe's manifest (dolby.manifest, embedded via the generated .syso)
// marks it detached: it uses the console of the terminal that starts it, and
// gets no console window when the logon task or Explorer starts it.
package main

//go:generate go run github.com/akavel/rsrc@v0.10.2 -manifest dolby.manifest -o rsrc_windows_amd64.syso

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"openfit-dolby/bt"
	"openfit-dolby/shokz"
)

// earbudsAddr is the Bluetooth address of the one pair of earbuds this tool
// was tested with (Shokz OpenFit 2+). It is the only address the tool will
// connect to; other earbuds have not been tested, so there is no option to
// choose another.
const earbudsAddr uint64 = 0xA00CE275632F

// dialFunc opens an RFCOMM stream to the earbuds at a Bluetooth address.
type dialFunc func(addr uint64) (io.ReadWriteCloser, error)

// Commands that do not talk to the earbuds themselves; tests replace them.
var (
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
	fs := flag.NewFlagSet("dolby", flag.ContinueOnError)
	fs.SetOutput(stderr)
	trace := fs.Bool("trace", false, "log every byte sent and received to stderr")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: dolby [-trace] status|on|off|tray|install|uninstall")
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "The tray always logs with -trace detail, to "+logPath())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	cmd := fs.Arg(0)
	switch cmd {
	case "status", "on", "off", "tray", "install", "uninstall":
	default:
		fmt.Fprintf(stderr, "dolby: unknown command %q\n", cmd)
		fs.Usage()
		return 2
	}
	switch cmd {
	case "tray":
		return startTray(dial, stderr)
	case "install":
		return runInstall(stdout, stderr)
	case "uninstall":
		return runUninstall(stdout, stderr)
	}

	var tr *bt.Tracer
	if *trace {
		tr = bt.NewTracer(stderr)
		tr.Logf("command %q, connecting to %012X", cmd, earbudsAddr)
	}
	conn, err := dial(earbudsAddr)
	if err != nil {
		if tr != nil {
			tr.Logf("connect failed: %v", err)
		}
		fmt.Fprintln(stderr, "dolby:", err)
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
		fmt.Fprintln(stderr, "dolby:", err)
		return 1
	}
	fmt.Fprintln(stdout, map[bool]string{true: "on", false: "off"}[on])
	return 0
}
