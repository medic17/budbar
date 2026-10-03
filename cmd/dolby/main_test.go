package main

import (
	"errors"
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

func TestParseAddr(t *testing.T) {
	for _, s := range []string{"A0:0C:E2:75:63:2F", "a00ce275632f"} {
		if v, err := parseAddr(s); err != nil || v != 0xA00CE275632F {
			t.Errorf("parseAddr(%q) = %x, %v", s, v, err)
		}
	}
}
