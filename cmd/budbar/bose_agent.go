package main

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"openfit-dolby/bose"
	"openfit-dolby/bt"
)

// How often the tray asks a Bose device for its mode while it is connected;
// it does not report changes on its own. After a failed check it
// waits longer, so a refused connection is not retried every few seconds.
const (
	bosePollEvery   = 15 * time.Second
	bosePollBackoff = 60 * time.Second
)

// BoseStatus is what the tray shows for a Bose device: whether it is
// connected, the last mode read (empty until the first read), whether the
// last check failed, and a line saying when the mode was read or why not.
type BoseStatus struct {
	Connected bool
	Mode      string
	Failed    bool
	Detail    string
}

// boseShown picks which Bose device the tray shows: of those connected, the
// one that connected last. The zero value is ready to use.
type boseShown struct {
	seq    int
	status map[uint64]BoseStatus // by device address
	since  map[uint64]int        // seq at which each device last connected
}

// set records the status of the device at addr.
func (b *boseShown) set(addr uint64, s BoseStatus) {
	if b.status == nil {
		b.status, b.since = map[uint64]BoseStatus{}, map[uint64]int{}
	}
	if s.Connected && !b.status[addr].Connected {
		b.seq++
		b.since[addr] = b.seq
	}
	b.status[addr] = s
}

// current returns the address and status of the device to show; ok is false
// while none is connected.
func (b *boseShown) current() (addr uint64, s BoseStatus, ok bool) {
	last := 0
	for a, st := range b.status {
		if st.Connected && b.since[a] > last {
			addr, s, ok, last = a, st, true, b.since[a]
		}
	}
	return addr, s, ok
}

// boseAgent reads one Bose device's mode in the background: once soon after
// it connects, then every bosePollEvery while it stays connected. Each check
// opens its own connection and closes it when done, so the channel is free
// for the Bose app between checks. Mode names are kept per slot, so a routine
// check sends only the current-mode GET; a slot's name is fetched the first
// time that slot is seen, and again after RefreshModes.
type boseAgent struct {
	dev     bose.Device
	dial    dialFunc
	probe   func() (bool, error) // whether the device is connected
	log     io.Writer            // gets the -trace output of every check
	publish func(BoseStatus)
	now     func() time.Time
	// after runs f once d has passed and returns a function that cancels it;
	// tests replace it.
	after func(d time.Duration, f func()) (cancel func())

	mu      sync.Mutex
	status  BoseStatus
	names   map[int]string // mode name by slot; used only by the running check
	gen     int            // bumped on every connect and disconnect
	reading bool
	cancel  func() // cancels the scheduled check, if any
	running sync.WaitGroup
}

func newBoseAgent(dev bose.Device, dial dialFunc, probe func() (bool, error), log io.Writer, publish func(BoseStatus)) *boseAgent {
	return &boseAgent{
		dev: dev, dial: dial, probe: probe, log: log, publish: publish, now: time.Now,
		after: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
		names: map[int]string{},
	}
}

func (a *boseAgent) clock() string { return a.now().Format("15:04") }

func (a *boseAgent) logf(format string, args ...any) {
	fmt.Fprintf(a.log, format+"\n", args...)
}

func (a *boseAgent) Status() BoseStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Check asks Windows whether the device is connected and updates the status
// to match.
func (a *boseAgent) Check(why string) {
	ok, err := a.probe()
	if err != nil {
		a.logf("%v connection check (%s) failed: %v", a.dev, why, err)
	} else {
		a.logf("%v connection check (%s): connected=%t", a.dev, why, ok)
	}
	if ok {
		a.Connected(why, 0)
	} else {
		a.Disconnected(why)
	}
}

// Connected records that the device is connected and reads the mode after
// settle; the regular checks follow from that read.
func (a *boseAgent) Connected(why string, settle time.Duration) {
	a.mu.Lock()
	if !a.status.Connected {
		a.gen++
		a.status = BoseStatus{Connected: true, Detail: "connected " + a.clock() + ", reading..."}
	}
	a.scheduleLocked(settle, why)
	s := a.status
	a.mu.Unlock()
	a.publish(s)
}

// Disconnected shows the device as not connected and stops the checks.
func (a *boseAgent) Disconnected(why string) {
	a.mu.Lock()
	a.gen++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.status = BoseStatus{Detail: a.dev.String() + " not connected (" + why + ", " + a.clock() + ")"}
	s := a.status
	a.mu.Unlock()
	a.logf("%v not connected (%s)", a.dev, why)
	a.publish(s)
}

// Refresh reads the mode now, and with names set also fetches the current
// slot's name again. It reports whether it started: it refuses while the
// device is not connected or a check is running.
func (a *boseAgent) Refresh(why string, names bool) bool {
	a.mu.Lock()
	g := a.gen
	a.mu.Unlock()
	return a.start(why, names, g)
}

// Wait blocks until the running check, if any, has finished.
func (a *boseAgent) Wait() { a.running.Wait() }

// Stop cancels the scheduled check.
func (a *boseAgent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
}

// scheduleLocked replaces any scheduled check with one after d. The check is
// tied to the current generation, so a disconnect in between voids it.
func (a *boseAgent) scheduleLocked(d time.Duration, why string) {
	if a.cancel != nil {
		a.cancel()
	}
	g := a.gen
	a.cancel = a.after(d, func() { a.start(why, false, g) })
}

// start runs one check in the background if the device is still connected
// in generation g and no check is running.
func (a *boseAgent) start(why string, names bool, g int) bool {
	a.mu.Lock()
	if !a.status.Connected || g != a.gen {
		a.mu.Unlock()
		a.logf("%v check (%s) skipped: not connected", a.dev, why)
		return false
	}
	if a.reading {
		a.mu.Unlock()
		a.logf("%v check (%s) skipped: another check is running", a.dev, why)
		return false
	}
	a.reading = true
	a.running.Add(1)
	a.mu.Unlock()

	go func() {
		defer a.running.Done()
		s := a.perform(why, names)
		a.mu.Lock()
		a.reading = false
		if !a.status.Connected || g != a.gen {
			a.mu.Unlock()
			return // disconnected meanwhile; that status stands
		}
		if s.Failed && s.Mode == "" {
			s.Mode = a.status.Mode // keep the last mode known
		}
		a.status = s
		if s.Connected {
			next := bosePollEvery
			if s.Failed {
				next = bosePollBackoff
			}
			a.scheduleLocked(next, "poll")
		}
		a.mu.Unlock()
		a.publish(s)
	}()
	return true
}

// perform connects, reads the mode and its name, and returns the new status.
func (a *boseAgent) perform(why string, refreshNames bool) BoseStatus {
	tr := bt.NewTracer(a.log)
	tr.Logf("%v check (%s), connecting to %012X", a.dev, why, a.dev.Addr)
	conn, err := a.dial(a.dev.Addr)
	if err != nil {
		tr.Logf("connect failed: %v", err)
		return a.failed(err)
	}
	tr.Logf("connected")
	tr.RW = conn
	defer tr.Close()

	c := bose.NewClient(tr)
	idx, err := c.CurrentMode()
	if err != nil {
		tr.Logf("failed: %v", err)
		return a.failed(err)
	}
	if refreshNames {
		clear(a.names)
	}
	name, ok := a.names[idx]
	if !ok {
		if name, err = c.ModeName(idx); err != nil {
			tr.Logf("mode %d name failed: %v", idx, err)
			return a.failed(fmt.Errorf("mode %d name: %w", idx, err))
		}
		a.names[idx] = name
	}
	tr.Logf("%v mode %d: %s", a.dev, idx, name)
	return BoseStatus{Connected: true, Mode: name, Detail: "checked " + a.clock()}
}

// failed is the status after a failed check. The device stays connected
// unless the failure was that it is not.
func (a *boseAgent) failed(err error) BoseStatus {
	return BoseStatus{
		Connected: !errors.Is(err, bose.ErrNotConnected),
		Failed:    true,
		Detail:    a.clock() + " " + boseReason(a.dev, err),
	}
}

// boseReason fits an error from dev into the tray's hover text; the log
// keeps the full message.
func boseReason(dev bose.Device, err error) string {
	switch {
	case errors.Is(err, windows.WSAEHOSTDOWN):
		return "can't connect: is the Bose app open?"
	case errors.Is(err, bose.ErrNoReply):
		return "no reply from the " + dev.Name
	case errors.Is(err, bose.ErrNotConnected):
		return dev.Name + " not connected"
	}
	return shortReason(err)
}
