package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxLogBytes = 1 << 20

// appDir is where the installed exe and the tray's log live.
func appDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "budbar")
}

func logPath() string { return filepath.Join(appDir(), "budbar.log") }

// appLog is the tray's log: every line gets a wall-clock timestamp and goes
// to the log file and to echo. Writers must write whole lines, as the tracer
// and logf do.
type appLog struct {
	mu   sync.Mutex // the tray thread and the action goroutines both log
	f    *os.File
	echo *os.File // the console when started from a terminal; writes may fail
	now  func() time.Time
}

// openAppLog opens the log file, first dropping its older half if it has
// grown past maxLogBytes. Without a log file it still echoes.
func openAppLog(echo *os.File) *appLog {
	l := &appLog{echo: echo, now: time.Now}
	if err := os.MkdirAll(appDir(), 0o755); err == nil {
		trimLog(logPath())
		l.f, _ = os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	}
	return l
}

func (l *appLog) Write(p []byte) (int, error) {
	stamp := []byte(l.now().Format("2006-01-02 15:04:05.000  "))
	var out []byte
	for line := range bytes.SplitAfterSeq(p, []byte("\n")) {
		if len(line) > 0 {
			out = append(append(out, stamp...), line...)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Write(out)
	}
	if l.echo != nil {
		l.echo.Write(out)
	}
	return len(p), nil
}

func (l *appLog) Close() {
	if l.f != nil {
		l.f.Close()
	}
}

// trimLog keeps the log bounded by dropping the oldest half, cut at a line
// break, once the file passes maxLogBytes.
func trimLog(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= maxLogBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	half := len(data) / 2
	if i := bytes.IndexByte(data[half:], '\n'); i >= 0 {
		os.WriteFile(path, data[half+i+1:], 0o644)
	}
}
