package bt

import (
	"fmt"
	"io"
	"time"
)

// Tracer logs every byte that passes through a stream, with the time since
// the trace started. It passes the bytes through unchanged. Set RW before
// reading or writing.
type Tracer struct {
	RW    io.ReadWriteCloser
	log   io.Writer
	start time.Time
}

func NewTracer(log io.Writer) *Tracer {
	return &Tracer{log: log, start: time.Now()}
}

func (t *Tracer) Logf(format string, args ...any) {
	fmt.Fprintf(t.log, "%8.3fs  "+format+"\n", append([]any{time.Since(t.start).Seconds()}, args...)...)
}

// Write logs p before sending it, so the log shows it even if the send fails
// or panics.
func (t *Tracer) Write(p []byte) (int, error) {
	t.Logf("send %2d bytes: % x", len(p), p)
	n, err := t.RW.Write(p)
	if err != nil {
		t.Logf("send error after %d bytes: %v", n, err)
	}
	return n, err
}

func (t *Tracer) Read(p []byte) (int, error) {
	n, err := t.RW.Read(p)
	if n > 0 {
		t.Logf("recv %2d bytes: % x", n, p[:n])
	}
	if err != nil {
		t.Logf("recv error: %v", err)
	}
	return n, err
}

func (t *Tracer) Close() error {
	err := t.RW.Close()
	t.Logf("closed (err=%v)", err)
	return err
}
