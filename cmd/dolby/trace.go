package main

import (
	"fmt"
	"io"
	"time"
)

// tracer logs every byte that passes through an RFCOMM stream, with the
// time since the trace started. It passes the bytes through unchanged.
type tracer struct {
	rw    io.ReadWriteCloser
	log   io.Writer
	start time.Time
}

func newTracer(log io.Writer) *tracer {
	return &tracer{log: log, start: time.Now()}
}

func (t *tracer) logf(format string, args ...any) {
	fmt.Fprintf(t.log, "%8.3fs  "+format+"\n", append([]any{time.Since(t.start).Seconds()}, args...)...)
}

// Write logs p before sending it, so the log shows it even if the send fails
// or panics.
func (t *tracer) Write(p []byte) (int, error) {
	t.logf("send %2d bytes: % x", len(p), p)
	n, err := t.rw.Write(p)
	if err != nil {
		t.logf("send error after %d bytes: %v", n, err)
	}
	return n, err
}

func (t *tracer) Read(p []byte) (int, error) {
	n, err := t.rw.Read(p)
	if n > 0 {
		t.logf("recv %2d bytes: % x", n, p[:n])
	}
	if err != nil {
		t.logf("recv error: %v", err)
	}
	return n, err
}

func (t *tracer) Close() error {
	err := t.rw.Close()
	t.logf("closed (err=%v)", err)
	return err
}
