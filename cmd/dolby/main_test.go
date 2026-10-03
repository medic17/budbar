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
		{"-addr", "zz", "status"},
		{"-addr", "A0:0C:E2:75:63", "status"},
		{"-addr", "A0:0C:E2:75:63:2F:00", "status"},
		{"-addr", "", "on"},
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

func TestParseAddr(t *testing.T) {
	for _, s := range []string{"A0:0C:E2:75:63:2F", "a00ce275632f"} {
		if v, err := parseAddr(s); err != nil || v != 0xA00CE275632F {
			t.Errorf("parseAddr(%q) = %x, %v", s, v, err)
		}
	}
}
