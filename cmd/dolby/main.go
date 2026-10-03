// Command dolby reads or switches Dolby Audio on Shokz OpenFit 2+ earbuds
// over Bluetooth RFCOMM from Windows.
//
//	dolby [-addr A0:0C:E2:75:63:2F] status|on|off
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"openfit-dolby/shokz"
)

// dialFunc opens an RFCOMM stream to the earbuds at a Bluetooth address.
type dialFunc func(addr uint64) (io.ReadWriteCloser, error)

func main() {
	os.Exit(run(os.Args[1:], dialRFCOMM, os.Stdout, os.Stderr))
}

// run checks all arguments before dialing, so bad input never opens a
// connection. It returns the process exit code.
func run(args []string, dial dialFunc, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dolby", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "A0:0C:E2:75:63:2F", "earbuds' Bluetooth address")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: dolby [-addr XX:XX:XX:XX:XX:XX] status|on|off")
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
	if cmd != "status" && cmd != "on" && cmd != "off" {
		fmt.Fprintf(stderr, "dolby: unknown command %q\n", cmd)
		fs.Usage()
		return 2
	}
	bt, err := parseAddr(*addr)
	if err != nil {
		fmt.Fprintln(stderr, "dolby:", err)
		return 2
	}

	conn, err := dial(bt)
	if err != nil {
		fmt.Fprintln(stderr, "dolby:", err)
		return 1
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

// parseAddr reads a 12-hex-digit Bluetooth address, with or without colons.
func parseAddr(s string) (uint64, error) {
	h := strings.ReplaceAll(s, ":", "")
	v, err := strconv.ParseUint(h, 16, 48)
	if len(h) != 12 || err != nil {
		return 0, fmt.Errorf("bad Bluetooth address %q", s)
	}
	return v, nil
}
