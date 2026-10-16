package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppLogStampsEveryLine(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	echo, err := os.Create(filepath.Join(t.TempDir(), "echo"))
	if err != nil {
		t.Fatal(err)
	}
	l := openAppLog(echo)
	l.now = func() time.Time { return time.Date(2026, 10, 3, 14, 2, 3, 4e6, time.Local) }
	l.Write([]byte("one\ntwo\n"))
	l.Close()
	echo.Close()

	want := "2026-10-03 14:02:03.004  one\n2026-10-03 14:02:03.004  two\n"
	for _, path := range []string{logPath(), echo.Name()} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestTrimLogKeepsNewestHalf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	line := strings.Repeat("x", 99) + "\n"
	var b strings.Builder
	for b.Len() <= maxLogBytes {
		b.WriteString(line)
	}
	b.WriteString("last\n")
	os.WriteFile(path, []byte(b.String()), 0o644)

	trimLog(path)
	got, _ := os.ReadFile(path)
	if len(got) > maxLogBytes/2+len(line) || !strings.HasSuffix(string(got), "last\n") || !strings.HasPrefix(string(got), "xxx") {
		t.Errorf("trimmed to %d bytes, ends %q", len(got), got[len(got)-10:])
	}
}

func TestTrimLogLeavesSmallLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	os.WriteFile(path, []byte("small\n"), 0o644)
	trimLog(path)
	if got, _ := os.ReadFile(path); string(got) != "small\n" {
		t.Errorf("log changed to %q", got)
	}
}
