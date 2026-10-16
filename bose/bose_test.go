package bose

import (
	"bytes"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestOnlyReadRequestsAllowed(t *testing.T) {
	for _, p := range [][]byte{
		{0x1f, 0x03, 0x01, 0x00}, // GET current mode
		{0x1f, 0x01, 0x05, 0x00}, // START get all modes
	} {
		if err := CheckOutgoing(p); err != nil {
			t.Errorf("% x refused: %v", p, err)
		}
	}
	for _, p := range [][]byte{
		nil,
		{0x1f, 0x03, 0x05, 0x02, 0x00, 0x00}, // START current mode: switches mode
		{0x1f, 0x03, 0x05, 0x00},             // START current mode, no payload
		{0x1f, 0x09, 0x05, 0x00},             // START reset
		{0x1f, 0x01, 0x05, 0x01, 0x00},       // START get all, with payload
		{0x07, 0x04, 0x05, 0x00},             // START power
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
	for _, r := range []Request{GetCurrentMode, GetAllModes} {
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
// nil), and records what was written. Reading shortens the chunk slices, so
// shared fixtures go in as a copy.
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
	} {
		f := &fakeStream{chunks: [][]byte{tc.reply}}
		c := NewClient(f)
		idx, err := c.CurrentMode()
		if err != nil || idx != tc.idx {
			t.Errorf("% x: got %d, %v", tc.reply, idx, err)
			continue
		}
		// Presets are named without asking the earbuds.
		if name, err := c.ModeName(idx); err != nil || name != tc.name || len(f.written) != 1 {
			t.Errorf("mode %d: got %q, %v after %d writes", idx, name, err, len(f.written))
		}
	}
}

// getAllReply is what the earbuds sent after START get all modes, with
// "Work" in slot 4 and current, cut after Work's mode config.
var getAllReply = [][]byte{
	mustHex("1f 01 07 00"),
	mustHex("1f 00 03 05 31 2e 30 2e 30"),
	mustHex("1f 02 03 07 04 07 00 00 00 77 02"),
	mustHex("1f 03 03 01 04"),
	mustHex("1f 06 03 30 00 00 01 00 00 01 51 75 69 65 74 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 01"),
	mustHex("1f 06 03 30 01 00 02 00 00 01 41 77 61 72 65 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 02 0a 01 00 00 00 01"),
	mustHex("1f 06 03 30 02 00 22 00 00 01 49 6d 6d 65 72 73 69 6f 6e 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 02 00 00 01"),
	mustHex("1f 06 03 30 03 00 24 00 00 01 43 69 6e 65 6d 61 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 01 00 00 01"),
	mustHex("1f 06 03 30 04 00 0b 01 01 01 57 6f 72 6b 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 15 03 00 02 01 00 01"),
}

// workConfig is the payload of Work's captured mode config.
func workConfig() []byte {
	return bytes.Clone(getAllReply[len(getAllReply)-1][headerLen:])
}

func TestModeNameFromEarbuds(t *testing.T) {
	f := &fakeStream{chunks: slices.Clone(getAllReply)}
	name, err := NewClient(f).ModeName(4)
	if err != nil || name != "Work" {
		t.Fatalf("got %q, %v", name, err)
	}
	if len(f.written) != 1 || !bytes.Equal(f.written[0], packetGetAllModes) {
		t.Errorf("wrote % x", f.written)
	}
}

func TestModeNameMissingSlot(t *testing.T) {
	// Slot 5 is not in the reply: the stream runs dry and the read times out.
	_, err := NewClient(&fakeStream{chunks: slices.Clone(getAllReply)}).ModeName(5)
	if !errors.Is(err, ErrNoReply) {
		t.Errorf("err = %v, want ErrNoReply", err)
	}
}

func TestModeConfigName(t *testing.T) {
	full := workConfig()
	if len(full) != 48 {
		t.Fatalf("captured payload is %d bytes, want 48", len(full))
	}
	empty := workConfig()
	clear(empty[modeNameStart:modeNameEnd])
	if _, err := modeConfigName(empty); err == nil {
		t.Error("accepted an empty name")
	}
	if _, err := modeConfigName(full[:modeNameEnd-1]); err == nil {
		t.Error("accepted a short payload")
	}
	long := bytes.Clone(full)
	copy(long[modeNameStart:modeNameEnd], bytes.Repeat([]byte("x"), 32))
	if name, err := modeConfigName(long); err != nil || name != strings.Repeat("x", 32) {
		t.Errorf("32-byte name: got %q, %v", name, err)
	}
	bad := bytes.Clone(full)
	copy(bad[modeNameStart:], []byte{'a', 0xff, 'b', 0})
	if name, err := modeConfigName(bad); err != nil || name != "a?b" {
		t.Errorf("invalid UTF-8: got %q, %v", name, err)
	}
}

func TestGetAllErrorStopsTheWait(t *testing.T) {
	_, err := NewClient(&fakeStream{chunks: [][]byte{mustHex("1f 01 04 01 05")}}).ModeName(4)
	if err == nil || !strings.Contains(err.Error(), "get all modes") {
		t.Errorf("err = %v", err)
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
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

func TestReadPacketSendsNothing(t *testing.T) {
	f := &fakeStream{chunks: [][]byte{{0x1f, 0x03}, {0x03, 0x01, 0x02}}}
	c := NewClient(f)
	p, err := c.ReadPacket()
	if err != nil || p.String() != "[31.3] op 3: 02" {
		t.Errorf("got %v, %v", p, err)
	}
	if _, err := c.ReadPacket(); !errors.Is(err, ErrNoReply) {
		t.Errorf("empty stream: err = %v, want ErrNoReply", err)
	}
	if len(f.written) != 0 {
		t.Errorf("wrote % x", f.written)
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
