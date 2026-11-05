package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"openfit-dolby/bose"
	"openfit-dolby/devices"
	"openfit-dolby/devices/devicestest"
)

// useDevices makes added the device list run picks from; none is connected.
func useDevices(t *testing.T, added ...devices.Device) {
	oldStore, oldConnected := store, connected
	t.Cleanup(func() { store, connected = oldStore, oldConnected })
	store = devicestest.Store(t, devicestest.AllPaired, added...)
	connected = func(uint64) (bool, error) { return false, nil }
}

type fakeConn struct {
	reply  *bytes.Reader
	sent   bytes.Buffer
	closed bool
}

func (f *fakeConn) Read(p []byte) (int, error)  { return f.reply.Read(p) }
func (f *fakeConn) Write(p []byte) (int, error) { return f.sent.Write(p) }
func (f *fakeConn) Close() error                { f.closed = true; return nil }

func TestRunMode(t *testing.T) {
	useDevices(t, devicestest.BoseBudsDevice)
	// A Cinema reply: presets are named without a second request.
	conn := &fakeConn{reply: bytes.NewReader([]byte{0x1f, 0x03, 0x03, 0x01, 0x03})}
	var dialed []uint64
	dial := func(addr uint64) (io.ReadWriteCloser, error) { dialed = append(dialed, addr); return conn, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-trace", "mode"}, dial, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != "Cinema\n" || len(dialed) != 1 || dialed[0] != devicestest.BoseBudsDevice.Addr || !conn.closed {
		t.Errorf("stdout %q, dialed %X, closed %v", stdout.String(), dialed, conn.closed)
	}
	if !strings.Contains(stderr.String(), "recv  5 bytes: 1f 03 03 01 03") {
		t.Errorf("trace missing reply: %q", stderr.String())
	}
}

func TestRunDeviceHeadphones(t *testing.T) {
	useDevices(t, devicestest.BoseBudsDevice, devicestest.BoseHPDevice)
	conn := &fakeConn{reply: bytes.NewReader([]byte{0x1f, 0x03, 0x03, 0x01, 0x00})}
	var dialed []uint64
	dial := func(addr uint64) (io.ReadWriteCloser, error) { dialed = append(dialed, addr); return conn, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-device", "headphones", "mode"}, dial, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != "Quiet\n" || len(dialed) != 1 || dialed[0] != devicestest.BoseHPDevice.Addr {
		t.Errorf("stdout %q, dialed %X", stdout.String(), dialed)
	}
}

func TestRunWatchPrintsUntilClosed(t *testing.T) {
	useDevices(t, devicestest.BoseBudsDevice)
	conn := &fakeConn{reply: bytes.NewReader([]byte{
		0x1f, 0x03, 0x03, 0x01, 0x04, // reply to the GET
		0x1f, 0x03, 0x03, 0x01, 0x00, // pushed by the earbuds
	})}
	dial := func(uint64) (io.ReadWriteCloser, error) { return conn, nil }
	var stdout, stderr bytes.Buffer
	code := run([]string{"watch"}, dial, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "closed") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "mode 4\n") || !strings.Contains(out, "[31.3] op 3: 00\n") {
		t.Errorf("stdout %q", out)
	}
	if conn.sent.Len() != 4 {
		t.Errorf("sent % x, want only the GET", conn.sent.Bytes())
	}
}

func TestRunWatchStopsAtDeadline(t *testing.T) {
	useDevices(t, devicestest.BoseBudsDevice)
	defer func(old func() time.Time) { now = old }(now)
	t0 := time.Now()
	calls := 0
	// Every call to the clock moves it a minute on, so the first check
	// after the start is past a 90 s deadline.
	now = func() time.Time { calls++; return t0.Add(time.Duration(calls) * time.Minute) }
	conn := &fakeConn{reply: bytes.NewReader([]byte{0x1f, 0x03, 0x03, 0x01, 0x04})}
	dial := func(uint64) (io.ReadWriteCloser, error) { return conn, nil }
	var stderr bytes.Buffer
	if code := run([]string{"-for", "90s", "watch"}, dial, io.Discard, &stderr); code != 0 {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestRunBadArgsNeverDial(t *testing.T) {
	useDevices(t, devicestest.BoseBudsDevice)
	dial := func(uint64) (io.ReadWriteCloser, error) { t.Fatal("dialed"); return nil, nil }
	for _, args := range [][]string{
		nil, {"status"}, {"mode", "x"}, {"-x", "mode"},
	} {
		if code := run(args, dial, io.Discard, io.Discard); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}

func TestRunNotConnected(t *testing.T) {
	useDevices(t, devicestest.BoseBudsDevice)
	dial := func(uint64) (io.ReadWriteCloser, error) { return nil, bose.ErrNotConnected }
	var stderr bytes.Buffer
	if code := run([]string{"mode"}, dial, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "not connected") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

// A -device that names no single added Bose device exits 1 without dialing.
func TestRunDeviceChoiceNeverDials(t *testing.T) {
	dial := func(uint64) (io.ReadWriteCloser, error) { t.Fatal("dialed"); return nil, nil }
	for _, tc := range []struct {
		added []devices.Device
		args  []string
	}{
		{nil, []string{"mode"}},
		{[]devices.Device{devicestest.ShokzDevice}, []string{"mode"}},
		{devicestest.AllTestedDevices, []string{"-device", "shokz", "mode"}},
		{devicestest.AllTestedDevices, []string{"-device", "jbl", "mode"}},
		// Both Bose devices added and neither connected.
		{devicestest.AllTestedDevices, []string{"mode"}},
	} {
		useDevices(t, tc.added...)
		var stderr bytes.Buffer
		if code := run(tc.args, dial, io.Discard, &stderr); code != 1 || stderr.Len() == 0 {
			t.Errorf("%v %q: exit %d, stderr %q", tc.added, tc.args, code, stderr.String())
		}
	}
}
