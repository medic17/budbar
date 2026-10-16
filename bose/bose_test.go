package bose

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestOnlyGetCurrentModeAllowed(t *testing.T) {
	if err := CheckOutgoing([]byte{0x1f, 0x03, 0x01, 0x00}); err != nil {
		t.Errorf("GET current mode refused: %v", err)
	}
	for _, p := range [][]byte{
		nil,
		{0x1f, 0x03, 0x05, 0x02, 0x00, 0x00}, // START current mode: switches mode
		{0x1f, 0x03, 0x02, 0x00},             // SETGET current mode
		{0x1f, 0x03, 0x01, 0x00, 0x00},       // trailing byte
		{0x1f, 0x03, 0x41, 0x00},             // same operator, other device bits
		{0x02, 0x02, 0x01, 0x00},             // GET battery: harmless, but not allowed yet
	} {
		if CheckOutgoing(p) == nil {
			t.Errorf("% x allowed", p)
		}
	}
}

func TestRequestPacketsPassTheGuard(t *testing.T) {
	for _, r := range []Request{GetCurrentMode} {
		MustAllow(r.packet())
	}
}

func TestMustAllowPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	MustAllow([]byte{0x1f, 0x03, 0x05, 0x02, 0x00, 0x00})
}

func TestNext(t *testing.T) {
	buf := []byte{0x1f, 0x03, 0x03, 0x01, 0x04, 0x02, 0x02}
	p, n, err := Next(buf)
	if err != nil || n != 5 || p.Block != 31 || p.Func != 3 || p.Op != OpStatus || !bytes.Equal(p.Payload, []byte{4}) {
		t.Fatalf("got %v, %d, %v", p, n, err)
	}
	if _, _, err := Next(buf[n:]); !errors.Is(err, ErrShort) {
		t.Errorf("partial header: err = %v, want ErrShort", err)
	}
	if _, _, err := Next([]byte{0x1f, 0x03, 0x03, 0x02, 0x04}); !errors.Is(err, ErrShort) {
		t.Errorf("partial payload: err = %v, want ErrShort", err)
	}
	if _, _, err := Next([]byte{0x1f, 0x03, 0x08, 0x00}); err == nil || errors.Is(err, ErrShort) {
		t.Errorf("unknown operator: err = %v", err)
	}
	// Upper flag bits are device and port numbers, not part of the operator.
	if p, _, err := Next([]byte{0x1f, 0x03, 0x43, 0x00}); err != nil || p.Op != OpStatus {
		t.Errorf("flags 0x43: got %v, %v", p, err)
	}
}

// fakeStream returns each chunk from one Read, then readErr (ErrNoReply if
// nil), and records what was written.
type fakeStream struct {
	chunks  [][]byte
	readErr error
	written [][]byte
}

func (f *fakeStream) Read(p []byte) (int, error) {
	if len(f.chunks) == 0 {
		if f.readErr != nil {
			return 0, f.readErr
		}
		return 0, ErrNoReply
	}
	n := copy(p, f.chunks[0])
	f.chunks[0] = f.chunks[0][n:]
	if len(f.chunks[0]) == 0 {
		f.chunks = f.chunks[1:]
	}
	return n, nil
}

func (f *fakeStream) Write(p []byte) (int, error) {
	f.written = append(f.written, bytes.Clone(p))
	return len(p), nil
}

func TestCurrentMode(t *testing.T) {
	f := &fakeStream{chunks: [][]byte{
		{0x02, 0x02, 0x03, 0x01, 0x50}, // unrelated STATUS first
		{0x1f, 0x03},                   // reply split across reads
		{0x03, 0x01, 0x04},
	}}
	mode, err := NewClient(f).CurrentMode()
	if err != nil || mode != 4 {
		t.Fatalf("got %d, %v", mode, err)
	}
	if len(f.written) != 1 || !bytes.Equal(f.written[0], packetGetCurrentMode) {
		t.Errorf("wrote % x", f.written)
	}
}

// Replies captured from the earbuds while switching modes on them.
func TestCapturedModeReplies(t *testing.T) {
	for _, tc := range []struct {
		reply []byte
		idx   int
		name  string
	}{
		{[]byte{0x1f, 0x03, 0x03, 0x01, 0x00}, 0, "Quiet"},
		{[]byte{0x1f, 0x03, 0x03, 0x01, 0x01}, 1, "Aware"},
		{[]byte{0x1f, 0x03, 0x03, 0x01, 0x02}, 2, "Immersion"},
		{[]byte{0x1f, 0x03, 0x03, 0x01, 0x03}, 3, "Cinema"},
		{[]byte{0x1f, 0x03, 0x03, 0x01, 0x04}, 4, "custom mode 4"}, // "Work"
	} {
		idx, err := NewClient(&fakeStream{chunks: [][]byte{tc.reply}}).CurrentMode()
		if err != nil || idx != tc.idx || ModeName(idx) != tc.name {
			t.Errorf("% x: got %d (%s), %v", tc.reply, idx, ModeName(idx), err)
		}
	}
}

// The status beacon on RFCOMM channel 14 sends this once a second. It is not
// BMAP; read as BMAP it decodes as one packet followed by garbage.
func TestBeaconIsNotAModeReply(t *testing.T) {
	beacon := []byte{0xff, 0x55, 0x02, 0x00, 0xee, 0x10}
	_, err := NewClient(&fakeStream{chunks: [][]byte{beacon, beacon}}).CurrentMode()
	if err == nil {
		t.Error("beacon accepted as a mode reply")
	}
}

func TestCurrentModeErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		f     *fakeStream
		want  string
		isErr error
	}{
		{"error reply", &fakeStream{chunks: [][]byte{{0x1f, 0x03, 0x04, 0x01, 0x05}}}, "error 05", nil},
		{"empty reply", &fakeStream{chunks: [][]byte{{0x1f, 0x03, 0x03, 0x00}}}, "empty reply", nil},
		{"no reply", &fakeStream{}, "", ErrNoReply},
		{"closed", &fakeStream{readErr: errors.New("x"), chunks: [][]byte{}}, "x", nil},
		{"garbage", &fakeStream{chunks: [][]byte{{0x1f, 0x03, 0x0f, 0x00}}}, "unknown operator", nil},
	} {
		_, err := NewClient(tc.f).CurrentMode()
		if err == nil || !strings.Contains(err.Error(), tc.want) || (tc.isErr != nil && !errors.Is(err, tc.isErr)) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestChattyStreamGivesUp(t *testing.T) {
	var noise [][]byte
	for range maxPerRequest/5 + 100 {
		noise = append(noise, []byte{0x02, 0x02, 0x03, 0x01, 0x50})
	}
	f := &fakeStream{chunks: noise}
	_, err := NewClient(f).CurrentMode()
	if err == nil || !strings.Contains(err.Error(), "within") {
		t.Errorf("err = %v, want the byte cap", err)
	}
	if len(f.chunks) == 0 {
		t.Error("read the whole stream instead of stopping at the cap")
	}
}
