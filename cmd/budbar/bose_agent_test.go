package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"openfit-dolby/bose"
)

// boseBuds answers like the real earbuds: a mode index for the current-mode
// GET, and for GetAll the PROCESSING reply and one mode config per slot with
// its name. It records the requests as "mode" and "all".
type boseBuds struct {
	mu      sync.Mutex
	mode    int
	names   []string // by slot
	sent    []string
	pending []byte
}

func modeConfig(slot int, name string) []byte {
	payload := make([]byte, 48)
	payload[0] = byte(slot)
	copy(payload[6:38], name)
	return append([]byte{0x1f, 0x06, 0x03, 48}, payload...)
}

func (b *boseBuds) Write(p []byte) (int, error) {
	if err := bose.CheckOutgoing(p); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case bytes.Equal(p, []byte{0x1f, 0x03, 0x01, 0x00}):
		b.sent = append(b.sent, "mode")
		b.pending = append(b.pending, 0x1f, 0x03, 0x03, 0x01, byte(b.mode))
	default: // GetAll, the only other packet CheckOutgoing allows
		b.sent = append(b.sent, "all")
		b.pending = append(b.pending, 0x1f, 0x01, 0x07, 0x00)
		for slot, name := range b.names {
			b.pending = append(b.pending, modeConfig(slot, name)...)
		}
	}
	return len(p), nil
}

func (b *boseBuds) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		return 0, bose.ErrNoReply
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *boseBuds) Close() error { return nil }

func (b *boseBuds) requests() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.Join(b.sent, " ")
	b.sent = nil
	return s
}

// fakeTimers records scheduled checks instead of running them.
type fakeTimers struct {
	mu   sync.Mutex
	list []*fakeTimer
}

type fakeTimer struct {
	d         time.Duration
	f         func()
	cancelled bool
}

func (ft *fakeTimers) after(d time.Duration, f func()) func() {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{d: d, f: f}
	ft.list = append(ft.list, t)
	return func() { ft.mu.Lock(); t.cancelled = true; ft.mu.Unlock() }
}

// pending returns the one live timer, failing the test if there is not
// exactly one.
func (ft *fakeTimers) pending(t *testing.T) *fakeTimer {
	t.Helper()
	ft.mu.Lock()
	defer ft.mu.Unlock()
	var live []*fakeTimer
	for _, x := range ft.list {
		if !x.cancelled {
			live = append(live, x)
		}
	}
	if len(live) != 1 {
		t.Fatalf("%d scheduled checks, want 1", len(live))
	}
	return live[0]
}

// fire runs the pending check and waits for it, marking it used.
func (ft *fakeTimers) fire(t *testing.T, a *boseAgent) time.Duration {
	t.Helper()
	x := ft.pending(t)
	ft.mu.Lock()
	x.cancelled = true
	ft.mu.Unlock()
	x.f()
	a.Wait()
	return x.d
}

func newTestBoseAgent(dial dialFunc) (*boseAgent, *fakeTimers) {
	return newTestBoseAgentFor(bose.Earbuds, dial)
}

func newTestBoseAgentFor(dev bose.Device, dial dialFunc) (*boseAgent, *fakeTimers) {
	ft := &fakeTimers{}
	a := newBoseAgent(dev, dial, func() (bool, error) { return true, nil }, io.Discard, func(BoseStatus) {})
	a.after = ft.after
	a.now = func() time.Time { return time.Date(2026, 10, 16, 14, 30, 0, 0, time.Local) }
	return a, ft
}

var budsNames = []string{"Quiet", "Aware", "Immersion", "Cinema", "Work"}

func TestBosePollingCachesNames(t *testing.T) {
	buds := &boseBuds{mode: 4, names: budsNames}
	a, ft := newTestBoseAgent(func(uint64) (io.ReadWriteCloser, error) { return buds, nil })

	a.Connected("earbuds connected", 5*time.Second)
	if s := a.Status(); !s.Connected || s.Mode != "" || !strings.Contains(s.Detail, "reading") {
		t.Errorf("before the first read: %+v", s)
	}
	if d := ft.fire(t, a); d != 5*time.Second {
		t.Errorf("first read after %v, want the settle time", d)
	}
	if s, r := a.Status(), buds.requests(); s.Mode != "Work" || s.Failed || r != "mode all" {
		t.Errorf("first read: %+v, requests %q", s, r)
	}

	// Later checks come every 15 s and use the cached name.
	if d := ft.fire(t, a); d != bosePollEvery {
		t.Errorf("next check after %v", d)
	}
	if r := buds.requests(); r != "mode" {
		t.Errorf("second check sent %q, want only the mode GET", r)
	}

	// A preset needs no name fetch.
	buds.mode = 0
	ft.fire(t, a)
	if s, r := a.Status(), buds.requests(); s.Mode != "Quiet" || r != "mode" {
		t.Errorf("preset: %+v, requests %q", s, r)
	}

	// Refresh modes fetches the current custom slot's name again.
	buds.mode = 4
	buds.names = []string{"Quiet", "Aware", "Immersion", "Cinema", "Office"}
	ft.fire(t, a)
	if s, r := a.Status(), buds.requests(); s.Mode != "Work" || r != "mode" {
		t.Errorf("before refreshing names: %+v, requests %q", s, r)
	}
	if !a.Refresh("menu", true) {
		t.Fatal("Refresh refused")
	}
	a.Wait()
	if s, r := a.Status(), buds.requests(); s.Mode != "Office" || r != "mode all" {
		t.Errorf("after refreshing names: %+v, requests %q", s, r)
	}
	// The refresh replaced the scheduled check instead of adding one.
	if d := ft.pending(t).d; d != bosePollEvery {
		t.Errorf("after refresh, next check in %v", d)
	}
}

func TestBoseFailureBacksOffAndKeepsMode(t *testing.T) {
	buds := &boseBuds{mode: 1, names: budsNames}
	var fail error
	a, ft := newTestBoseAgent(func(uint64) (io.ReadWriteCloser, error) {
		if fail != nil {
			return nil, fail
		}
		return buds, nil
	})
	a.Connected("startup", 0)
	ft.fire(t, a)

	fail = windows.WSAEHOSTDOWN
	ft.fire(t, a)
	s := a.Status()
	if !s.Connected || !s.Failed || s.Mode != "Aware" || !strings.Contains(s.Detail, "Bose app") {
		t.Errorf("after a refused connect: %+v", s)
	}
	if d := ft.pending(t).d; d != bosePollBackoff {
		t.Errorf("after a failure, next check in %v", d)
	}

	fail = nil
	ft.fire(t, a)
	if s := a.Status(); s.Failed || s.Mode != "Aware" {
		t.Errorf("after recovering: %+v", s)
	}
	if d := ft.pending(t).d; d != bosePollEvery {
		t.Errorf("after recovering, next check in %v", d)
	}
}

func TestBoseDisconnectStopsChecks(t *testing.T) {
	buds := &boseBuds{mode: 2, names: budsNames}
	a, ft := newTestBoseAgent(func(uint64) (io.ReadWriteCloser, error) { return buds, nil })
	a.Connected("startup", 0)
	ft.fire(t, a)
	buds.requests()

	stale := ft.pending(t)
	a.Disconnected("disconnected")
	if s := a.Status(); s.Connected || s.Mode != "" {
		t.Errorf("after disconnect: %+v", s)
	}
	if !stale.cancelled {
		t.Error("scheduled check not cancelled")
	}
	stale.f() // a timer that fired just before the cancel
	a.Wait()
	if r := buds.requests(); r != "" {
		t.Errorf("sent %q after disconnect", r)
	}
	if a.Refresh("menu", false) {
		t.Error("Refresh started while disconnected")
	}
}

func TestBoseNotConnectedDuringCheck(t *testing.T) {
	a, ft := newTestBoseAgent(func(uint64) (io.ReadWriteCloser, error) { return nil, bose.ErrNotConnected })
	a.Connected("startup", 0)
	ft.fire(t, a)
	if s := a.Status(); s.Connected {
		t.Errorf("status %+v, want not connected", s)
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	for _, x := range ft.list {
		if !x.cancelled {
			t.Errorf("check scheduled after %v though the earbuds are gone", x.d)
		}
	}
}

func TestBoseReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{windows.WSAEHOSTDOWN, "Bose app"},
		{bose.ErrNoReply, "no reply from the headphones"},
		{bose.ErrNotConnected, "headphones not connected"},
		{errors.New("something else"), "something else"},
	} {
		if got := boseReason(bose.Headphones, tc.err); !strings.Contains(got, tc.want) {
			t.Errorf("boseReason(%v) = %q", tc.err, got)
		}
	}
}

// The headphones' agent dials their address and names them in its log.
func TestBoseAgentUsesItsDevice(t *testing.T) {
	buds := &boseBuds{mode: 3, names: budsNames}
	var dialed []uint64
	a, ft := newTestBoseAgentFor(bose.Headphones, func(addr uint64) (io.ReadWriteCloser, error) {
		dialed = append(dialed, addr)
		return buds, nil
	})
	var log bytes.Buffer
	a.log = &log
	a.Connected("headphones connected", 0)
	ft.fire(t, a)
	if s := a.Status(); s.Mode != "Cinema" || len(dialed) != 1 || dialed[0] != bose.Headphones.Addr {
		t.Errorf("status %+v, dialed %X", s, dialed)
	}
	if !strings.Contains(log.String(), "Bose headphones mode 3: Cinema") {
		t.Errorf("log %q", log.String())
	}
	a.Disconnected("disconnected")
	if s := a.Status(); !strings.HasPrefix(s.Detail, "Bose headphones not connected") {
		t.Errorf("after disconnect: %+v", s)
	}
}

func TestBoseShownPicksLastConnected(t *testing.T) {
	buds, hp := bose.Earbuds.Addr, bose.Headphones.Addr
	var b boseShown
	if _, _, ok := b.current(); ok {
		t.Fatal("zero value shows a device")
	}
	want := func(step string, addr uint64, mode string) {
		t.Helper()
		got, s, ok := b.current()
		if !ok || got != addr || s.Mode != mode {
			t.Errorf("%s: showing %012X %+v (ok %t), want %012X %q", step, got, s, ok, addr, mode)
		}
	}

	b.set(buds, BoseStatus{Connected: true, Mode: "Work"})
	want("earbuds on", buds, "Work")
	b.set(hp, BoseStatus{Connected: true})
	want("headphones on too", hp, "")
	// A later check of the earbuds does not take the tray back.
	b.set(buds, BoseStatus{Connected: true, Mode: "Quiet"})
	b.set(hp, BoseStatus{Connected: true, Mode: "Aware"})
	want("both polled", hp, "Aware")
	b.set(hp, BoseStatus{Detail: "not connected"})
	want("headphones off", buds, "Quiet")
	// Reconnecting makes the headphones the latest again.
	b.set(hp, BoseStatus{Connected: true, Mode: "Cinema"})
	want("headphones back", hp, "Cinema")
	b.set(hp, BoseStatus{})
	b.set(buds, BoseStatus{})
	if _, _, ok := b.current(); ok {
		t.Error("shows a device while none is connected")
	}
}
