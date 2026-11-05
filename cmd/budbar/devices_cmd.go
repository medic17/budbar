package main

// The devices, add and remove commands. They only read Windows' records and
// change devices.json; none of them connects to a device.

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"openfit-dolby/devices"
)

// describe is a device's line in listings: name, address and model.
func describe(d devices.Device) string {
	m, ok := devices.ModelByID(d.Model)
	model := m.Name
	if !ok {
		model = "unknown model " + d.Model
	}
	return fmt.Sprintf("%s\t%s\t%s", d.Name, devices.FormatAddr(d.Addr), model)
}

// listDevices prints the added devices and those that can be added.
func listDevices(stdout, stderr io.Writer) int {
	list, err := store.Load()
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	cands, err := store.Candidates()
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, part := range []struct {
		title string
		list  []devices.Device
	}{{"Added:", list}, {"Can be added (paired, of a tested model):", cands}} {
		fmt.Fprintln(w, part.title)
		if len(part.list) == 0 {
			fmt.Fprintln(w, "  (none)")
		}
		for _, d := range part.list {
			fmt.Fprintln(w, "  "+describe(d))
		}
	}
	return flushed(w, stderr)
}

func flushed(w *tabwriter.Writer, stderr io.Writer) int {
	if err := w.Flush(); err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	return 0
}

// pickOne returns the one device in list that query names, or prints why
// there is none.
func pickOne(list []devices.Device, query, what string, stderr io.Writer) (devices.Device, bool) {
	m := devices.Match(list, query)
	switch len(m) {
	case 1:
		return m[0], true
	case 0:
		fmt.Fprintf(stderr, "budbar: no %s matches %q (budbar devices lists them)\n", what, query)
	default:
		w := tabwriter.NewWriter(stderr, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "budbar: %d devices match %q; use more of the name, or the address:\n", len(m), query)
		for _, d := range m {
			fmt.Fprintln(w, "  "+describe(d))
		}
		w.Flush()
	}
	return devices.Device{}, false
}

// addDevice adds the one paired device of a tested model that query names.
func addDevice(query string, stdout, stderr io.Writer) int {
	cands, err := store.Candidates()
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	d, ok := pickOne(cands, query, "paired device of a tested model that is not added yet", stderr)
	if !ok {
		return 1
	}
	if err := store.Add(d); err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	fmt.Fprintln(stdout, "added   "+strings.ReplaceAll(describe(d), "\t", "  "))
	return 0
}

// removeDevice removes the one added device that query names.
func removeDevice(query string, stdout, stderr io.Writer) int {
	list, err := store.Load()
	if err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	d, ok := pickOne(list, query, "added device", stderr)
	if !ok {
		return 1
	}
	if err := store.Remove(d.Addr); err != nil {
		fmt.Fprintln(stderr, "budbar:", err)
		return 1
	}
	fmt.Fprintln(stdout, "removed "+strings.ReplaceAll(describe(d), "\t", "  "))
	return 0
}
