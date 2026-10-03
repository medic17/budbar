package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// noDial fails the test if run tries to connect.
func noDial(t *testing.T) dialFunc {
	return func(addr uint64) (io.ReadWriteCloser, error) {
		t.Errorf("dialed %012x", addr)
		return nil, errors.New("test does not allow dialing")
	}
}

func TestBadInputNeverDials(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"onn"},
		{"ON"},
		{"status", "extra"},
		{"-x", "status"},
		// There is no way to choose another device.
		{"-addr", "A0:0C:E2:75:63:2F", "status"},
		{"-addr", "11:22:33:44:55:66", "on"},
		{"tray", "extra"},
		{"install", "extra"},
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
		`command "status", connecting to A00CE275632F`,
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

// The CLI dials only the tested earbuds.
func TestDialsOnlyTheEarbuds(t *testing.T) {
	var dialed []uint64
	dial := func(addr uint64) (io.ReadWriteCloser, error) {
		dialed = append(dialed, addr)
		return &fakeConn{reply: replyGetOff}, nil
	}
	for _, cmd := range []string{"status", "off"} {
		run([]string{cmd}, dial, io.Discard, io.Discard)
	}
	if fmt.Sprint(dialed) != fmt.Sprint([]uint64{earbudsAddr, earbudsAddr}) {
		t.Errorf("dialed %x", dialed)
	}
}

func TestNotConnectedExits1(t *testing.T) {
	var stderr strings.Builder
	dial := func(uint64) (io.ReadWriteCloser, error) { return nil, errNotConnected }
	if code := run([]string{"on"}, dial, io.Discard, &stderr); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not connected to this PC") {
		t.Errorf("stderr %q", stderr.String())
	}
}
