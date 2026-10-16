package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCheckTarget(t *testing.T) {
	if err := checkTarget(budsAddr); err != nil {
		t.Errorf("tested earbuds refused: %v", err)
	}
	for _, addr := range []uint64{0, budsAddr ^ 1, 0xA00CE275632F, 0x68F21F0DF042} {
		if checkTarget(addr) == nil {
			t.Errorf("%012X allowed", addr)
		}
	}
}

// The address guard runs first: another address panics before the
// connection check or any socket.
func TestDialPanicsForOtherAddress(t *testing.T) {
	checked := false
	defer func(old func() (bool, error)) { connectedCheck = old }(connectedCheck)
	connectedCheck = func() (bool, error) { checked = true; return true, nil }
	defer func() {
		if recover() == nil {
			t.Error("dialRFCOMM did not panic")
		}
		if checked {
			t.Error("checked the connection before refusing the address")
		}
	}()
	dialRFCOMM(budsAddr ^ 1)
}

func TestDialRefusesWhenNotConnected(t *testing.T) {
	defer func(old func() (bool, error)) { connectedCheck = old }(connectedCheck)
	connectedCheck = func() (bool, error) { return false, nil }
	if _, err := dialRFCOMM(budsAddr); !errors.Is(err, errNotConnected) {
		t.Errorf("err = %v, want errNotConnected", err)
	}
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
	// A Cinema reply: presets are named without a second request.
	conn := &fakeConn{reply: bytes.NewReader([]byte{0x1f, 0x03, 0x03, 0x01, 0x03})}
	var dialed []uint64
	dial := func(addr uint64) (io.ReadWriteCloser, error) { dialed = append(dialed, addr); return conn, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-trace", "mode"}, dial, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != "Cinema\n" || len(dialed) != 1 || dialed[0] != budsAddr || !conn.closed {
		t.Errorf("stdout %q, dialed %X, closed %v", stdout.String(), dialed, conn.closed)
	}
	if !strings.Contains(stderr.String(), "recv  5 bytes: 1f 03 03 01 03") {
		t.Errorf("trace missing reply: %q", stderr.String())
	}
}

func TestRunWatchPrintsUntilClosed(t *testing.T) {
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
	dial := func(uint64) (io.ReadWriteCloser, error) { t.Fatal("dialed"); return nil, nil }
	for _, args := range [][]string{nil, {"status"}, {"mode", "x"}, {"-x", "mode"}} {
		if code := run(args, dial, io.Discard, io.Discard); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}

func TestRunNotConnected(t *testing.T) {
	dial := func(uint64) (io.ReadWriteCloser, error) { return nil, errNotConnected }
	var stderr bytes.Buffer
	if code := run([]string{"mode"}, dial, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "not connected") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}
