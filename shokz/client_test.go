package shokz

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"testing"
)

// fakeEarbuds stands in for the RFCOMM stream. It records every frame the
// client writes and queues the reply that respond returns for it.
type fakeEarbuds struct {
	on      bool
	sent    [][]byte
	pending []byte
	// chunk limits how many bytes one Read returns; 0 means no limit.
	chunk int
	// respond picks the reply to a written frame; nil means honest.
	respond func(f *fakeEarbuds, frame []byte) []byte
	// readErr is returned once nothing is pending; nil means ErrNoReply.
	readErr error
}

// honest answers like the real earbuds did in the capture.
func honest(f *fakeEarbuds, frame []byte) []byte {
	switch {
	case bytes.Equal(frame, frameGetDolby):
		if f.on {
			return replyGetOn
		}
		return replyGetOff
	case bytes.Equal(frame, frameSetOn):
		f.on = true
		return replySet
	case bytes.Equal(frame, frameSetOff):
		f.on = false
		return replySet
	}
	return nil
}

func (f *fakeEarbuds) Write(p []byte) (int, error) {
	f.sent = append(f.sent, bytes.Clone(p))
	respond := f.respond
	if respond == nil {
		respond = honest
	}
	f.pending = append(f.pending, respond(f, p)...)
	return len(p), nil
}

func (f *fakeEarbuds) Read(p []byte) (int, error) {
	if len(f.pending) == 0 {
		if f.readErr != nil {
			return 0, f.readErr
		}
		return 0, ErrNoReply
	}
	if f.chunk > 0 && f.chunk < len(p) {
		p = p[:f.chunk]
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

// requests names the frames the client sent, in order.
func (f *fakeEarbuds) requests() []Request {
	var rs []Request
	for _, p := range f.sent {
		r := Request(0)
		for _, k := range []Request{GetDolby, SetOn, SetOff} {
			if bytes.Equal(p, k.frame()) {
				r = k
			}
		}
		rs = append(rs, r)
	}
	return rs
}

func expectSent(t *testing.T, f *fakeEarbuds, want ...Request) {
	t.Helper()
	if got := f.requests(); !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

func TestStatus(t *testing.T) {
	for _, on := range []bool{true, false} {
		f := &fakeEarbuds{on: on}
		got, err := NewClient(f).Status()
		if err != nil || got != on {
			t.Errorf("Status() = %v, %v; want %v", got, err, on)
		}
		expectSent(t, f, GetDolby)
	}
}

func TestSet(t *testing.T) {
	for _, tc := range []struct {
		from, to bool
		wantSent bool
		want     []Request
	}{
		{false, true, true, []Request{GetDolby, SetOn, GetDolby}},
		{true, false, true, []Request{GetDolby, SetOff, GetDolby}},
		{true, true, false, []Request{GetDolby}},
		{false, false, false, []Request{GetDolby}},
	} {
		f := &fakeEarbuds{on: tc.from}
		sent, err := NewClient(f).Set(tc.to)
		if err != nil || sent != tc.wantSent || f.on != tc.to {
			t.Errorf("%v -> %v: sent=%v err=%v earbuds on=%v", tc.from, tc.to, sent, err, f.on)
		}
		expectSent(t, f, tc.want...)
	}
}

func TestStatusHandlesAwkwardReplies(t *testing.T) {
	unrelated := makeFrame(true, OpGet, 0x8031, []byte{1, 0, 0, 0})
	withExtra := func(before ...[]byte) func(*fakeEarbuds, []byte) []byte {
		return func(f *fakeEarbuds, frame []byte) []byte {
			return append(slices.Concat(before...), honest(f, frame)...)
		}
	}
	for name, f := range map[string]*fakeEarbuds{
		"one byte per read":        {chunk: 1},
		"seven bytes per read":     {chunk: 7},
		"junk before the reply":    {respond: withExtra([]byte{0, 1, 0xa5, 2})},
		"unrelated feature first":  {respond: withExtra(unrelated)},
		"set reply first":          {respond: withExtra(replySet)},
		"two unrelated, split":     {respond: withExtra(unrelated, unrelated), chunk: 13},
		"reply to a request frame": {respond: withExtra(frameGetDolby)},
	} {
		on, err := NewClient(f).Status()
		if err != nil || on {
			t.Errorf("%s: Status() = %v, %v; want off", name, on, err)
		}
		expectSent(t, f, GetDolby)
	}
}

func TestStatusRejectsBadReplies(t *testing.T) {
	badCRC := bytes.Clone(replyGetOff)
	badCRC[len(badCRC)-2] ^= 1
	for name, reply := range map[string][]byte{
		"bad checksum":  badCRC,
		"unknown state": makeFrame(true, OpGet, FeatureDolby|responseFeature, []byte{0, 2, 1, 0}),
		"short value":   makeFrame(true, OpGet, FeatureDolby|responseFeature, []byte{0, 1}),
	} {
		f := &fakeEarbuds{respond: func(*fakeEarbuds, []byte) []byte { return reply }}
		if _, err := NewClient(f).Status(); err == nil {
			t.Errorf("%s: no error", name)
		}
		expectSent(t, f, GetDolby)
	}
}

// Each failure must end with an error and never with a second set request.
func TestSetFailures(t *testing.T) {
	setOnly := func(reply []byte, changeState bool) func(*fakeEarbuds, []byte) []byte {
		return func(f *fakeEarbuds, frame []byte) []byte {
			if bytes.Equal(frame, frameGetDolby) {
				return honest(f, frame)
			}
			if changeState {
				f.on = true
			}
			return reply
		}
	}
	for _, tc := range []struct {
		name string
		f    *fakeEarbuds
		want []Request
	}{
		{"no reply to get", &fakeEarbuds{respond: func(*fakeEarbuds, []byte) []byte { return nil }},
			[]Request{GetDolby}},
		{"no reply to set", &fakeEarbuds{respond: setOnly(nil, false)},
			[]Request{GetDolby, SetOn}},
		{"hang-up after set", &fakeEarbuds{respond: setOnly(nil, false), readErr: io.EOF},
			[]Request{GetDolby, SetOn}},
		{"set rejected", &fakeEarbuds{respond: setOnly(makeFrame(true, OpSet, FeatureDolby|responseFeature, []byte{1, 0, 0, 0}), false)},
			[]Request{GetDolby, SetOn}},
		{"set ignored", &fakeEarbuds{respond: setOnly(replySet, false)},
			[]Request{GetDolby, SetOn, GetDolby}},
		{"set reply without value", &fakeEarbuds{respond: setOnly(makeFrame(true, OpSet, FeatureDolby|responseFeature, nil), true)},
			[]Request{GetDolby, SetOn}},
	} {
		if _, err := NewClient(tc.f).Set(true); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
		expectSent(t, tc.f, tc.want...)
	}
}

func TestNoReplyErrorIsReported(t *testing.T) {
	f := &fakeEarbuds{respond: func(*fakeEarbuds, []byte) []byte { return nil }}
	if _, err := NewClient(f).Status(); !errors.Is(err, ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
}

func TestEndlessUnrelatedFramesStop(t *testing.T) {
	unrelated := makeFrame(true, OpGet, 0x8031, []byte{1, 0, 0, 0})
	flood := bytes.Repeat(unrelated, 2*maxPerRequest/len(unrelated))
	f := &fakeEarbuds{respond: func(*fakeEarbuds, []byte) []byte { return flood }}
	if _, err := NewClient(f).Status(); err == nil {
		t.Fatal("no error")
	}
	expectSent(t, f, GetDolby)
}

func TestOneSetPerClient(t *testing.T) {
	f := &fakeEarbuds{}
	c := NewClient(f)
	if _, err := c.Set(true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Set(false); err == nil {
		t.Fatal("second set allowed")
	}
	expectSent(t, f, GetDolby, SetOn, GetDolby, GetDolby)
}

func TestToggle(t *testing.T) {
	for _, tc := range []struct {
		from bool
		want []Request
	}{
		{false, []Request{GetDolby, SetOn, GetDolby}},
		{true, []Request{GetDolby, SetOff, GetDolby}},
	} {
		f := &fakeEarbuds{on: tc.from}
		on, err := NewClient(f).Toggle()
		if err != nil || on == tc.from || f.on == tc.from {
			t.Errorf("toggle from %v: got %v, err=%v, earbuds on=%v", tc.from, on, err, f.on)
		}
		expectSent(t, f, tc.want...)
	}
}

func TestToggleFailures(t *testing.T) {
	ignoreSet := func(f *fakeEarbuds, frame []byte) []byte {
		if bytes.Equal(frame, frameGetDolby) {
			return honest(f, frame)
		}
		return replySet
	}
	for _, tc := range []struct {
		name string
		f    *fakeEarbuds
		want []Request
	}{
		{"no reply to get", &fakeEarbuds{respond: func(*fakeEarbuds, []byte) []byte { return nil }},
			[]Request{GetDolby}},
		{"set ignored", &fakeEarbuds{respond: ignoreSet},
			[]Request{GetDolby, SetOn, GetDolby}},
	} {
		if _, err := NewClient(tc.f).Toggle(); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
		expectSent(t, tc.f, tc.want...)
	}
}

// Set and Toggle share the one-set limit.
func TestOneSetPerClientAcrossToggle(t *testing.T) {
	f := &fakeEarbuds{}
	c := NewClient(f)
	if _, err := c.Toggle(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Toggle(); err == nil {
		t.Fatal("second toggle allowed")
	}
	if _, err := c.Set(false); err == nil {
		t.Fatal("set after toggle allowed")
	}
	expectSent(t, f, GetDolby, SetOn, GetDolby, GetDolby, GetDolby)
}

func TestSendPanicsOnUnknownRequest(t *testing.T) {
	f := &fakeEarbuds{}
	defer func() {
		if recover() == nil {
			t.Error("send did not panic")
		}
		if len(f.sent) != 0 {
			t.Errorf("wrote % x", f.sent)
		}
	}()
	NewClient(f).send(Request(99))
}
