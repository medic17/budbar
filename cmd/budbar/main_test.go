package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"openfit-dolby/bt"
	"openfit-dolby/devices"
	"openfit-dolby/devices/devicestest"
)

// useDevices makes added the device list run picks from, with every tested
// device and two untested ones paired; none is connected.
func useDevices(t *testing.T, added ...devices.Device) devices.Store {
	oldStore, oldConnected := store, connected
	t.Cleanup(func() { store, connected = oldStore, oldConnected })
	store = devicestest.Store(t, devicestest.AllPaired, added...)
	connected = func(uint64) (bool, error) { return false, nil }
	return store
}

// noDial fails the test if run tries to connect.
func noDial(t *testing.T) dialFunc {
	return func(addr uint64) (io.ReadWriteCloser, error) {
		t.Errorf("dialed %012x", addr)
		return nil, errors.New("test does not allow dialing")
	}
}

func TestBadInputNeverDials(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	for _, args := range [][]string{
		{},
		{"onn"},
		{"ON"},
		{"status", "extra"},
		{"-x", "status"},
		// Devices are added by name; there is no address option.
		{"-addr", "A0:0C:E2:75:63:2F", "status"},
		{"tray", "extra"},
		{"install", "extra"},
		{"devices", "extra"},
		{"add"},
		{"remove"},
		{"-device", "shokz", "tray"},
		{"-device", "shokz", "add", "bose"},
	} {
		var stderr strings.Builder
		if code := run(args, noDial(t), io.Discard, &stderr); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if stderr.Len() == 0 {
			t.Errorf("%q: no error message", args)
		}
	}
}

func TestHelpNeverDials(t *testing.T) {
	if code := run([]string{"-h"}, noDial(t), io.Discard, io.Discard); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDialErrorExits1(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	var stderr strings.Builder
	dial := func(uint64) (io.ReadWriteCloser, error) { return nil, errors.New("connect: refused") }
	if code := run([]string{"status"}, dial, io.Discard, &stderr); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "connect: refused") {
		t.Errorf("stderr %q does not explain the failure", stderr.String())
	}
}

// replyGetOff is the earbuds' captured answer to a get while Dolby is off.
var replyGetOff, _ = hex.DecodeString(strings.ReplaceAll("a5 5a 30 00 01 01 00 00 2c 00 b3 72 01 00 00 00 "+
	"24 00 00 00 02 00 00 00 04 00 00 00 02 00 00 00 03 00 00 00 04 00 00 00 2b 80 00 00 04 00 00 00 "+
	"04 00 00 00 00 00 01 00", " ", ""))

// fakeConn answers every write with reply and records what it was sent.
type fakeConn struct {
	reply   []byte
	sent    [][]byte
	pending []byte
	closed  bool
}

func (f *fakeConn) Write(p []byte) (int, error) {
	f.sent = append(f.sent, bytes.Clone(p))
	f.pending = append(f.pending, f.reply...)
	return len(p), nil
}

func (f *fakeConn) Read(p []byte) (int, error) {
	if len(f.pending) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func TestStatus(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	conn := &fakeConn{reply: replyGetOff}
	var stdout, stderr strings.Builder
	dial := func(uint64) (io.ReadWriteCloser, error) { return conn, nil }
	if code := run([]string{"status"}, dial, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != "off\n" || stderr.Len() != 0 {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	if len(conn.sent) != 1 || !conn.closed {
		t.Errorf("sent %d frames, closed=%v; want 1 frame and closed", len(conn.sent), conn.closed)
	}
}

func TestTraceLogsTrafficUnchanged(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	conn := &fakeConn{reply: replyGetOff}
	var stdout, stderr strings.Builder
	dial := func(uint64) (io.ReadWriteCloser, error) { return conn, nil }
	if code := run([]string{"-trace", "status"}, dial, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != "off\n" {
		t.Errorf("stdout %q, want off", stdout.String())
	}
	log := stderr.String()
	for _, want := range []string{
		`command "status", connecting to OpenFit 2+ by Shokz at A00CE275632F`,
		"connected",
		fmt.Sprintf("send %2d bytes: % x", len(conn.sent[0]), conn.sent[0]),
		fmt.Sprintf("recv %2d bytes: % x", len(replyGetOff), replyGetOff),
		"closed (err=<nil>)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("trace lacks %q\ntrace:\n%s", want, log)
		}
	}
}

func TestTraceLogsConnectFailure(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	var stderr strings.Builder
	dial := func(uint64) (io.ReadWriteCloser, error) { return nil, errors.New("connect: refused") }
	run([]string{"-trace", "status"}, dial, io.Discard, &stderr)
	if !strings.Contains(stderr.String(), "connect failed: connect: refused") {
		t.Errorf("trace %q lacks the connect failure", stderr.String())
	}
}

// tray, install and uninstall hand over to their own code without dialing.
func TestNonDialingCommands(t *testing.T) {
	var got []string
	oldTray, oldInstall, oldUninstall := startTray, runInstall, runUninstall
	defer func() { startTray, runInstall, runUninstall = oldTray, oldInstall, oldUninstall }()
	startTray = func(dialFunc, io.Writer) int {
		got = append(got, "tray")
		return 0
	}
	runInstall = func(_, _ io.Writer) int {
		got = append(got, "install")
		return 0
	}
	runUninstall = func(_, _ io.Writer) int {
		got = append(got, "uninstall")
		return 0
	}
	for _, args := range [][]string{{"tray"}, {"install"}, {"uninstall"}} {
		if code := run(args, noDial(t), io.Discard, io.Discard); code != 0 {
			t.Errorf("%q: exit %d", args, code)
		}
	}
	want := []string{"tray", "install", "uninstall"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// The CLI dials only the added earbuds.
func TestDialsOnlyTheEarbuds(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	var dialed []uint64
	dial := func(addr uint64) (io.ReadWriteCloser, error) {
		dialed = append(dialed, addr)
		return &fakeConn{reply: replyGetOff}, nil
	}
	for _, cmd := range []string{"status", "off"} {
		run([]string{cmd}, dial, io.Discard, io.Discard)
	}
	if fmt.Sprint(dialed) != fmt.Sprint([]uint64{devicestest.ShokzDevice.Addr, devicestest.ShokzDevice.Addr}) {
		t.Errorf("dialed %x", dialed)
	}
}

func TestNotConnectedExits1(t *testing.T) {
	useDevices(t, devicestest.ShokzDevice)
	var stderr strings.Builder
	dial := func(uint64) (io.ReadWriteCloser, error) { return nil, errNotConnected }
	if code := run([]string{"on"}, dial, io.Discard, &stderr); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not connected to this PC") {
		t.Errorf("stderr %q", stderr.String())
	}
}

// Without a single added Shokz device to use, status, on and off exit 1
// without dialing.
func TestNoShokzChosenNeverDials(t *testing.T) {
	second := devices.Device{Addr: 0xA00CE2750000, Model: devicestest.ShokzDevice.Model, Name: "OpenFit 2+ by Shokz"}
	for _, tc := range []struct {
		added []devices.Device
		args  []string
	}{
		{nil, []string{"status"}},
		{[]devices.Device{devicestest.BoseBudsDevice}, []string{"on"}},
		{devicestest.AllTestedDevices, []string{"-device", "bose", "status"}},
		{devicestest.AllTestedDevices, []string{"-device", "jbl", "off"}},
	} {
		useDevices(t, tc.added...)
		var stderr strings.Builder
		if code := run(tc.args, noDial(t), io.Discard, &stderr); code != 1 || stderr.Len() == 0 {
			t.Errorf("%v %q: exit %d, stderr %q", tc.added, tc.args, code, stderr.String())
		}
	}
	// Two added Shokz earbuds, neither connected: -device must pick one.
	paired := append(slices.Clone(devicestest.AllPaired), bt.PairedDevice{
		Addr: second.Addr, Name: second.Name, VendorID: devicestest.ShokzPaired.VendorID, Services: devicestest.ShokzPaired.Services,
	})
	oldStore, oldConnected := store, connected
	t.Cleanup(func() { store, connected = oldStore, oldConnected })
	store = devicestest.Store(t, paired, devicestest.ShokzDevice, second)
	connected = func(uint64) (bool, error) { return false, nil }
	var stderr strings.Builder
	if code := run([]string{"status"}, noDial(t), io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "-device") {
		t.Errorf("two Shokz: exit %d, stderr %q", code, stderr.String())
	}
	var dialed []uint64
	dial := func(addr uint64) (io.ReadWriteCloser, error) {
		dialed = append(dialed, addr)
		return &fakeConn{reply: replyGetOff}, nil
	}
	if code := run([]string{"-device", "A0:0C:E2:75:00:00", "status"}, dial, io.Discard, io.Discard); code != 0 || fmt.Sprint(dialed) != fmt.Sprint([]uint64{second.Addr}) {
		t.Errorf("-device by address: exit %d, dialed %X", code, dialed)
	}
}

func TestDevicesAddRemove(t *testing.T) {
	s := useDevices(t)
	runOK := func(args ...string) string {
		t.Helper()
		var stdout, stderr strings.Builder
		if code := run(args, noDial(t), &stdout, &stderr); code != 0 {
			t.Fatalf("%q: exit %d, stderr %q", args, code, stderr.String())
		}
		return stdout.String()
	}
	runFails := func(want string, args ...string) {
		t.Helper()
		var stderr strings.Builder
		if code := run(args, noDial(t), io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), want) {
			t.Errorf("%q: exit %d, stderr %q; want exit 1 mentioning %q", args, code, stderr.String(), want)
		}
	}

	out := runOK("devices")
	want := `Added:
  (none)
Can be added (paired, of a tested model):
  Bose QC Ultra 2 Earbuds  68:F2:1F:3B:5C:B7  Bose QC Ultra Earbuds (2nd Gen)
  Bose QC Ultra 2 HP       68:F2:1F:0D:F0:42  Bose QC Ultra Headphones (2nd Gen)
  OpenFit 2+ by Shokz      A0:0C:E2:75:63:2F  Shokz OpenFit 2+
`
	if out != want {
		t.Errorf("devices:\n%s\nwant:\n%s", out, want)
	}

	runFails("2 devices match", "add", "bose")
	runFails("no paired device of a tested model", "add", "jbl")
	if out := runOK("add", "openfit"); !strings.Contains(out, "added   OpenFit 2+ by Shokz  A0:0C:E2:75:63:2F") {
		t.Errorf("add: %q", out)
	}
	runFails("no paired device of a tested model", "add", "openfit")
	runOK("add", "bose", "headphones")
	if list, _ := s.Load(); fmt.Sprint(list) != fmt.Sprint([]devices.Device{devicestest.ShokzDevice, devicestest.BoseHPDevice}) {
		t.Errorf("after adding: %v", list)
	}

	runFails("no added device", "remove", "earbuds")
	if out := runOK("remove", "shokz"); !strings.Contains(out, "removed OpenFit 2+ by Shokz") {
		t.Errorf("remove: %q", out)
	}
	if list, _ := s.Load(); fmt.Sprint(list) != fmt.Sprint([]devices.Device{devicestest.BoseHPDevice}) {
		t.Errorf("after removing: %v", list)
	}
}
