// Command bose reads the listening mode of Bose QC Ultra Earbuds (2nd Gen)
// over Bluetooth RFCOMM from Windows.
//
//	bose [-trace] [-for duration] mode|watch
//
// It talks only to the earbuds at budsAddr, only while they are connected to
// this PC, and sends only read requests. mode prints the active mode's name;
// for a mode made in the Bose app, the name stored on the earbuds. watch
// prints the active mode's index, then keeps the connection open and prints
// every packet the earbuds send for the -for duration.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"openfit-dolby/bose"
	"openfit-dolby/bt"
)

// budsAddr is the Bluetooth address of the one pair of earbuds this tool was
// tested with (Bose QC Ultra Earbuds, 2nd Gen). It is the only address the
// tool will connect to.
const budsAddr uint64 = 0x68F21F3B5CB7

// dialFunc opens an RFCOMM stream to the earbuds at a Bluetooth address.
type dialFunc func(addr uint64) (io.ReadWriteCloser, error)

func main() {
	os.Exit(run(os.Args[1:], dialRFCOMM, os.Stdout, os.Stderr))
}

// run checks all arguments before dialing, so bad input never opens a
// connection. It returns the process exit code.
func run(args []string, dial dialFunc, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	trace := fs.Bool("trace", false, "log every byte sent and received to stderr")
	watchFor := fs.Duration("for", 2*time.Minute, "how long watch listens")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: bose [-trace] [-for duration] mode|watch")
		fs.PrintDefaults()
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
	if cmd != "mode" && cmd != "watch" {
		fmt.Fprintf(stderr, "bose: unknown command %q\n", cmd)
		fs.Usage()
		return 2
	}

	var tr *bt.Tracer
	if *trace {
		tr = bt.NewTracer(stderr)
		tr.Logf("connecting to %012X", budsAddr)
	}
	conn, err := dial(budsAddr)
	if err != nil {
		if tr != nil {
			tr.Logf("connect failed: %v", err)
		}
		fmt.Fprintln(stderr, "bose:", err)
		return 1
	}
	if tr != nil {
		tr.Logf("connected")
		tr.RW = conn
		conn = tr
	}
	defer conn.Close()

	c := bose.NewClient(conn)
	mode, err := c.CurrentMode()
	if err != nil {
		fmt.Fprintln(stderr, "bose:", err)
		return 1
	}
	if cmd == "watch" {
		return watch(c, mode, *watchFor, stdout, stderr)
	}
	name, err := c.ModeName(mode)
	if err != nil {
		fmt.Fprintf(stderr, "bose: mode %d: %v\n", mode, err)
		return 1
	}
	fmt.Fprintln(stdout, name)
	return 0
}

// now is the clock watch uses; tests replace it.
var now = time.Now

// watch prints mode, then every packet the earbuds send on their own until d
// has passed. It sends nothing. The deadline is checked after each read, so
// it can run over by up to one read timeout.
func watch(c *bose.Client, mode int, d time.Duration, stdout, stderr io.Writer) int {
	start := now()
	stamp := func() string { return fmt.Sprintf("%8.3fs", now().Sub(start).Seconds()) }
	fmt.Fprintf(stdout, "%s  mode %d\n", stamp(), mode)
	for now().Sub(start) < d {
		p, err := c.ReadPacket()
		if errors.Is(err, bose.ErrNoReply) {
			continue
		}
		if err != nil {
			fmt.Fprintln(stderr, "bose:", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s  %v\n", stamp(), p)
	}
	return 0
}
