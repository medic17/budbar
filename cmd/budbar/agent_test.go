package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"openfit-dolby/shokz"
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

// Captured replies (see shokz/shokz_test.go).
var (
	replyGetOn = mustHex("a5 5a 30 00 01 01 00 00 2c 00 e2 b2 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 02 00 00 00 03 00 00 00 04 00 00 00 2b 80 00 00 04 00 00 00 04 00 00 00 00 01 01 00")
	replySet = mustHex("a5 5a 30 00 01 01 00 00 2c 00 b4 22 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 01 00 00 00 03 00 00 00 04 00 00 00 2b 80 00 00 04 00 00 00 04 00 00 00 00 00 00 00")
)

// earbuds answers like the real earbuds and records what it was sent, as
// "get", "set on" or "set off".
type earbuds struct {
	mu      sync.Mutex
	on      bool
	sent    []string
	pending []byte
	closed  int
}

func (e *earbuds) Write(p []byte) (int, error) {
	if err := shokz.CheckOutgoing(p); err != nil {
		return 0, err
	}
	m, err := shokz.Decode(p)
	if err != nil {
		return 0, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case m.Op == shokz.OpGet && e.on:
		e.sent, e.pending = append(e.sent, "get"), append(e.pending, replyGetOn...)
	case m.Op == shokz.OpGet:
		e.sent, e.pending = append(e.sent, "get"), append(e.pending, replyGetOff...)
	default:
		e.on = m.Value[0] == 1
		e.sent, e.pending = append(e.sent, "set "+map[bool]string{true: "on", false: "off"}[e.on]), append(e.pending, replySet...)
	}
	return len(p), nil
}

func (e *earbuds) Read(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pending) == 0 {
		return 0, shokz.ErrNoReply
	}
	n := copy(p, e.pending)
	e.pending = e.pending[n:]
	return n, nil
}

func (e *earbuds) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed++
	return nil
}

func (e *earbuds) requests() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sent)
}

// recorder collects every published Status.
type recorder struct {
	mu   sync.Mutex
	seen []Status
}

func (r *recorder) publish(s Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, s)
}

func (r *recorder) all() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

// testAgent returns an agent whose earbuds are already known to be connected.
func testAgent(dial dialFunc) (*agent, *recorder, *syncBuilder) {
	rec := &recorder{}
	log := &syncBuilder{}
	a := newAgent(dial, func() (bool, error) { return true, nil }, log, rec.publish)
	a.now = func() time.Time { return time.Date(2026, 10, 3, 14, 2, 0, 0, time.Local) }
	a.status.Connected = true
	return a, rec, log
}

// syncBuilder is a strings.Builder safe for the agent's goroutines.
type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func dialTo(e *earbuds) dialFunc {
	return func(uint64) (io.ReadWriteCloser, error) { return e, nil }
}

func TestAgentActions(t *testing.T) {
	for _, tc := range []struct {
		act   action
		from  bool
		want  dolbyState
		frame []string
	}{
		{actRefresh, false, stateOff, []string{"get"}},
		{actRefresh, true, stateOn, []string{"get"}},
		{actOn, false, stateOn, []string{"get", "set on", "get"}},
		{actOn, true, stateOn, []string{"get"}},
		{actOff, true, stateOff, []string{"get", "set off", "get"}},
		{actOff, false, stateOff, []string{"get"}},
		{actToggle, false, stateOn, []string{"get", "set on", "get"}},
		{actToggle, true, stateOff, []string{"get", "set off", "get"}},
	} {
		e := &earbuds{on: tc.from}
		a, rec, _ := testAgent(dialTo(e))
		if !a.Do(tc.act, "test") {
			t.Fatalf("%v: refused", tc.act)
		}
		a.Wait()
		name := fmt.Sprintf("%v from on=%v", tc.act, tc.from)
		if got := e.requests(); !slices.Equal(got, tc.frame) {
			t.Errorf("%s: sent %v, want %v", name, got, tc.frame)
		}
		if e.closed != 1 {
			t.Errorf("%s: closed %d times, want 1", name, e.closed)
		}
		want := []Status{{Connected: true, Busy: true}, {State: tc.want, Connected: true, Detail: "checked 14:02"}}
		if got := rec.all(); !slices.Equal(got, want) {
			t.Errorf("%s: published %+v, want %+v", name, got, want)
		}
	}
}

// While busy, the icon keeps the last known colour.
func TestAgentBusyKeepsLastState(t *testing.T) {
	e := &earbuds{on: true}
	a, rec, _ := testAgent(dialTo(e))
	a.Do(actRefresh, "first")
	a.Wait()
	a.Do(actToggle, "second")
	a.Wait()
	got := rec.all()
	if len(got) != 4 || got[2] != (Status{State: stateOn, Connected: true, Busy: true, Detail: "checked 14:02"}) || got[3].State != stateOff {
		t.Errorf("published %+v", got)
	}
}

func TestAgentRefusesWhileBusy(t *testing.T) {
	release := make(chan struct{})
	e := &earbuds{}
	dial := func(uint64) (io.ReadWriteCloser, error) {
		<-release
		return e, nil
	}
	a, _, log := testAgent(dial)
	if !a.Do(actToggle, "first") {
		t.Fatal("first action refused")
	}
	if a.Do(actToggle, "second") {
		t.Error("second action started while the first was running")
	}
	close(release)
	a.Wait()
	if got := e.requests(); !slices.Equal(got, []string{"get", "set on", "get"}) {
		t.Errorf("sent %v; want a single toggle", got)
	}
	if !strings.Contains(log.String(), "toggle (second) ignored") {
		t.Errorf("log does not record the refused action:\n%s", log.String())
	}
	if !a.Do(actRefresh, "after") {
		t.Error("refused after the first action finished")
	}
	a.Wait()
}

func TestAgentFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dial      dialFunc
		connected bool
		detail    string
	}{
		{"Shokz app open", func(uint64) (io.ReadWriteCloser, error) {
			return nil, connectError(windows.WSAEHOSTDOWN)
		}, true, "14:02 can't connect: is the Shokz app open?"},
		{"other connect error", func(uint64) (io.ReadWriteCloser, error) {
			return nil, errors.New("connect: unreachable")
		}, true, "14:02 connect: unreachable"},
		{"no reply", func(uint64) (io.ReadWriteCloser, error) {
			return &fakeConn{}, nil
		}, true, "14:02 earbuds closed the connection"},
		{"not connected", func(uint64) (io.ReadWriteCloser, error) {
			return nil, errNotConnected
		}, false, "14:02 earbuds not connected"},
	} {
		a, rec, log := testAgent(tc.dial)
		a.Do(actRefresh, "test")
		a.Wait()
		got := rec.all()
		want := Status{State: stateUnknown, Connected: tc.connected, Detail: tc.detail}
		if len(got) != 2 || got[1] != want {
			t.Errorf("%s: published %+v, want last %+v", tc.name, got, want)
		}
		if !strings.Contains(log.String(), "failed") {
			t.Errorf("%s: log lacks the failure:\n%s", tc.name, log.String())
		}
	}
}

func TestAgentLogsTraffic(t *testing.T) {
	e := &earbuds{}
	a, _, log := testAgent(dialTo(e))
	a.Do(actToggle, "Ctrl+Q")
	a.Wait()
	for _, want := range []string{
		"toggle (Ctrl+Q), connecting to A00CE275632F",
		"connected",
		fmt.Sprintf("recv %2d bytes: % x", len(replySet), replySet),
		"Dolby Audio on",
		"closed (err=<nil>)",
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q\n%s", want, log.String())
		}
	}
}

func TestRefreshLaterCoalesces(t *testing.T) {
	e := &earbuds{}
	a, _, _ := testAgent(dialTo(e))
	for range 5 {
		a.RefreshLater(20*time.Millisecond, "event")
	}
	time.Sleep(100 * time.Millisecond)
	a.Wait()
	if got := e.requests(); !slices.Equal(got, []string{"get"}) {
		t.Errorf("sent %v, want one get", got)
	}
	a.RefreshLater(10*time.Millisecond, "later event")
	time.Sleep(60 * time.Millisecond)
	a.Wait()
	if got := e.requests(); len(got) != 2 {
		t.Errorf("sent %v, want a second get after the first refresh ran", got)
	}
}

func TestAgentRefusesWhileDisconnected(t *testing.T) {
	a, rec, log := testAgent(noDial(t))
	a.Disconnected("disconnected")
	for _, act := range []action{actRefresh, actOn, actOff, actToggle} {
		if a.Do(act, "test") {
			t.Errorf("%v started while disconnected", act)
		}
	}
	a.Wait()
	want := Status{State: stateUnknown, Detail: "earbuds not connected (disconnected, 14:02)"}
	if got := rec.all(); !slices.Equal(got, []Status{want}) {
		t.Errorf("published %+v, want %+v", got, want)
	}
	if !strings.Contains(log.String(), "toggle (test) ignored: earbuds not connected") {
		t.Errorf("log lacks the refusal:\n%s", log.String())
	}
}

func TestAgentDisconnectDuringActionStaysGrey(t *testing.T) {
	release := make(chan struct{})
	e := &earbuds{}
	a, rec, _ := testAgent(func(uint64) (io.ReadWriteCloser, error) {
		<-release
		return e, nil
	})
	a.Do(actRefresh, "test")
	a.Disconnected("disconnected")
	close(release)
	a.Wait()
	got := rec.all()
	want := Status{State: stateUnknown, Detail: "earbuds not connected (disconnected, 14:02)"}
	if got[len(got)-1] != want {
		t.Errorf("last status %+v, want %+v", got[len(got)-1], want)
	}
}

func TestAgentCheck(t *testing.T) {
	for _, tc := range []struct {
		name     string
		probe    func() (bool, error)
		wantSent []string
		want     Status
	}{
		{"connected", func() (bool, error) { return true, nil },
			[]string{"get"}, Status{State: stateOff, Connected: true, Detail: "checked 14:02"}},
		{"not connected", func() (bool, error) { return false, nil },
			nil, Status{Detail: "earbuds not connected (startup, 14:02)"}},
		{"check failed", func() (bool, error) { return false, errors.New("no radio") },
			nil, Status{Detail: "earbuds not connected (startup, 14:02)"}},
	} {
		e := &earbuds{}
		a, rec, _ := testAgent(dialTo(e))
		a.status = Status{}
		a.probe = tc.probe
		a.Check("startup")
		time.Sleep(50 * time.Millisecond) // the read starts from a timer
		a.Wait()
		if got := e.requests(); !slices.Equal(got, tc.wantSent) {
			t.Errorf("%s: sent %v, want %v", tc.name, got, tc.wantSent)
		}
		if got := rec.all(); got[len(got)-1] != tc.want {
			t.Errorf("%s: last status %+v, want %+v", tc.name, got[len(got)-1], tc.want)
		}
	}
}

// After a connect event the icon stays grey until the delayed read.
func TestAgentConnectedReadsAfterSettle(t *testing.T) {
	e := &earbuds{on: true}
	a, rec, _ := testAgent(dialTo(e))
	a.Disconnected("disconnected")
	a.Connected("earbuds connected", 30*time.Millisecond)
	if got := e.requests(); len(got) != 0 {
		t.Fatalf("read before the settle time: %v", got)
	}
	if s := a.Status(); s.State != stateUnknown || !s.Connected {
		t.Errorf("status before the read %+v", s)
	}
	time.Sleep(100 * time.Millisecond)
	a.Wait()
	if got := rec.all(); got[len(got)-1] != (Status{State: stateOn, Connected: true, Detail: "checked 14:02"}) {
		t.Errorf("last status %+v", got[len(got)-1])
	}
}

func TestShortReasonTruncates(t *testing.T) {
	long := errors.New(strings.Repeat("é", 200))
	if got := []rune(shortReason(long)); len(got) != 80 || !bytes.HasSuffix([]byte(string(got)), []byte("...")) {
		t.Errorf("shortReason = %q (%d runes)", string(got), len(got))
	}
}
