package main

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"openfit-dolby/shokz"
)

// dolbyState is what the tray last learned about Dolby Audio.
type dolbyState int

const (
	stateUnknown dolbyState = iota
	stateOff
	stateOn
)

func (s dolbyState) String() string {
	switch s {
	case stateOff:
		return "off"
	case stateOn:
		return "on"
	}
	return "unknown"
}

// Status is what the tray shows: the state, whether the earbuds are
// connected to this PC (actions need that), whether an action is running,
// and a line saying when the state was read or why it is not known.
type Status struct {
	State     dolbyState
	Connected bool
	Busy      bool
	Detail    string
}

type action int

const (
	actRefresh action = iota + 1
	actOn
	actOff
	actToggle
)

func (a action) String() string {
	switch a {
	case actRefresh:
		return "refresh"
	case actOn:
		return "turn on"
	case actOff:
		return "turn off"
	case actToggle:
		return "toggle"
	}
	return fmt.Sprintf("action(%d)", int(a))
}

// agent runs the tray's actions, one at a time and only while the earbuds
// are connected to this PC. Each action opens its own connection and Client
// and closes it when done, so it sends at most one set request and never
// holds the earbuds' only connection while idle.
type agent struct {
	dial    dialFunc
	probe   func() (bool, error) // whether the earbuds are connected
	log     io.Writer            // gets the -trace output of every action
	publish func(Status)
	now     func() time.Time

	mu             sync.Mutex
	status         Status
	refreshPending bool
	running        sync.WaitGroup
}

func newAgent(dial dialFunc, probe func() (bool, error), log io.Writer, publish func(Status)) *agent {
	return &agent{dial: dial, probe: probe, log: log, publish: publish, now: time.Now}
}

func (a *agent) clock() string { return a.now().Format("15:04") }

func (a *agent) logf(format string, args ...any) {
	fmt.Fprintf(a.log, format+"\n", args...)
}

func (a *agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Do starts act in the background and reports whether it did. It refuses
// while the earbuds are not connected, and while another action runs, so
// repeated clicks or key presses cannot queue up.
func (a *agent) Do(act action, why string) bool {
	a.mu.Lock()
	if !a.status.Connected {
		a.mu.Unlock()
		a.logf("%s (%s) ignored: earbuds not connected", act, why)
		return false
	}
	if a.status.Busy {
		a.mu.Unlock()
		a.logf("%s (%s) ignored: another action is running", act, why)
		return false
	}
	a.status.Busy = true
	busy := a.status
	a.running.Add(1)
	a.mu.Unlock()
	a.publish(busy)

	go func() {
		defer a.running.Done()
		s := a.perform(act, why)
		a.mu.Lock()
		if a.status.Connected {
			a.status = s
		} else {
			a.status.Busy = false // they disconnected meanwhile; stay grey
		}
		s = a.status
		a.mu.Unlock()
		a.publish(s)
	}()
	return true
}

// Check asks Windows whether the earbuds are connected and updates the
// status to match: it reads the state if they are, and shows them as not
// connected if they are not or the check fails.
func (a *agent) Check(why string) {
	ok, err := a.probe()
	if err != nil {
		a.logf("connection check (%s) failed: %v", why, err)
	} else {
		a.logf("connection check (%s): connected=%t", why, ok)
	}
	if ok {
		a.Connected(why, 0)
	} else {
		a.Disconnected(why)
	}
}

// Connected records that the earbuds are connected and reads the state after
// settle. Until that read the state stays as it was.
func (a *agent) Connected(why string, settle time.Duration) {
	a.mu.Lock()
	if !a.status.Connected {
		a.status.Connected = true
		a.status.Detail = "connected " + a.clock() + ", reading..."
		s := a.status
		a.mu.Unlock()
		a.publish(s)
	} else {
		a.mu.Unlock()
	}
	a.RefreshLater(settle, why)
}

// Disconnected shows the earbuds as not connected, which greys the icon and
// stops actions until they connect again.
func (a *agent) Disconnected(why string) {
	a.mu.Lock()
	a.status.Connected = false
	a.status.State = stateUnknown
	a.status.Detail = "earbuds not connected (" + why + ", " + a.clock() + ")"
	s := a.status
	a.mu.Unlock()
	a.logf("earbuds not connected (%s)", why)
	a.publish(s)
}

// RefreshLater reads the state after d. Calls made while one is already
// waiting add nothing, so a burst of events causes a single read.
func (a *agent) RefreshLater(d time.Duration, why string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refreshPending {
		return
	}
	a.refreshPending = true
	time.AfterFunc(d, func() {
		a.mu.Lock()
		a.refreshPending = false
		a.mu.Unlock()
		a.Do(actRefresh, why)
	})
}

// Wait blocks until the running action, if any, has finished.
func (a *agent) Wait() { a.running.Wait() }

func (a *agent) perform(act action, why string) Status {
	tr := newTracer(a.log)
	tr.logf("%s (%s), connecting to %012X", act, why, earbudsAddr)
	conn, err := a.dial(earbudsAddr)
	if err != nil {
		tr.logf("connect failed: %v", err)
		return a.failed(err)
	}
	tr.logf("connected")
	tr.rw = conn
	defer tr.Close()

	c := shokz.NewClient(tr)
	var on bool
	switch act {
	case actRefresh:
		on, err = c.Status()
	case actOn, actOff:
		on = act == actOn
		_, err = c.Set(on)
	case actToggle:
		on, err = c.Toggle()
	default:
		err = fmt.Errorf("unknown action %v", act)
	}
	if err != nil {
		tr.logf("failed: %v", err)
		return a.failed(err)
	}
	s := Status{State: stateOff, Connected: true, Detail: "checked " + a.clock()}
	if on {
		s.State = stateOn
	}
	tr.logf("Dolby Audio %s", s.State)
	return s
}

// failed is the status after a failed action. The earbuds stay connected
// unless the failure was that they are not.
func (a *agent) failed(err error) Status {
	return Status{
		State:     stateUnknown,
		Connected: !errors.Is(err, errNotConnected),
		Detail:    a.clock() + " " + shortReason(err),
	}
}

// shortReason fits an error into the tray's hover text; the log keeps the
// full message.
func shortReason(err error) string {
	switch {
	case errors.Is(err, windows.WSAEHOSTDOWN):
		return "can't connect: is the Shokz app open?"
	case errors.Is(err, shokz.ErrNoReply):
		return "no reply from the earbuds"
	case errors.Is(err, errNotConnected):
		return "earbuds not connected"
	}
	const max = 80
	r := []rune(err.Error())
	if len(r) > max {
		return string(r[:max-3]) + "..."
	}
	return string(r)
}
