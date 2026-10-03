package shokz

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
)

// Replies captured from the earbuds alongside the requests in shokz.go.
var (
	replySet = mustHex("a5 5a 30 00 01 01 00 00 2c 00 b4 22 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 01 00 00 00 03 00 00 00 04 00 00 00 2b 80 00 00 04 00 00 00 04 00 00 00 00 00 00 00")
	replyGetOn = mustHex("a5 5a 30 00 01 01 00 00 2c 00 e2 b2 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 02 00 00 00 03 00 00 00 04 00 00 00 2b 80 00 00 04 00 00 00 04 00 00 00 00 01 01 00")
	replyGetOff = mustHex("a5 5a 30 00 01 01 00 00 2c 00 b3 72 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 02 00 00 00 03 00 00 00 04 00 00 00 2b 80 00 00 04 00 00 00 04 00 00 00 00 00 01 00")
)

// makeFrame builds a well-formed frame for tests that need replies the
// capture does not contain. The client never sends frames built this way;
// CheckOutgoing would refuse them.
func makeFrame(response bool, op, feature uint32, value []byte) []byte {
	le := binary.LittleEndian
	var fields []byte
	fields = le.AppendUint32(le.AppendUint32(le.AppendUint32(fields, tlvOp), 4), op)
	fields = le.AppendUint32(le.AppendUint32(le.AppendUint32(fields, tlvFeature), 4), feature)
	fields = append(le.AppendUint32(le.AppendUint32(fields, tlvValue), uint32(len(value))), value...)
	body := append(le.AppendUint32(le.AppendUint32(nil, 1), uint32(len(fields))), fields...)

	f := []byte{0xa5, 0x5a}
	f = le.AppendUint16(f, uint16(len(body)+4))
	dir := byte(0)
	if response {
		dir = 1
	}
	f = append(f, 1, dir, 0, 0)
	f = le.AppendUint16(f, uint16(len(body)))
	f = le.AppendUint16(f, CRC16(body))
	return append(f, body...)
}

func TestMakeFrameMatchesCapture(t *testing.T) {
	if got := makeFrame(true, OpGet, FeatureDolby|responseFeature, []byte{0, 1, 1, 0}); !bytes.Equal(got, replyGetOn) {
		t.Fatalf("makeFrame = % x\nwant        % x", got, replyGetOn)
	}
}

func TestCapturedRequests(t *testing.T) {
	for _, tc := range []struct {
		req   Request
		len   int
		op    uint32
		value []byte
	}{
		{GetDolby, 52, OpGet, []byte{}},
		{SetOn, 56, OpSet, []byte{1, 0, 0, 0}},
		{SetOff, 56, OpSet, []byte{0, 0, 0, 0}},
	} {
		f := tc.req.frame()
		if len(f) != tc.len {
			t.Errorf("%s: %d bytes, want %d", tc.req, len(f), tc.len)
		}
		m, err := Decode(f)
		if err != nil {
			t.Fatalf("%s: %v", tc.req, err)
		}
		if m.Response || m.Op != tc.op || m.Feature != FeatureDolby || !bytes.Equal(m.Value, tc.value) {
			t.Errorf("%s: decoded %+v", tc.req, m)
		}
	}
}

func TestOnOffDifferOnlyInValueAndChecksum(t *testing.T) {
	var diff []int
	for i := range frameSetOn {
		if frameSetOn[i] != frameSetOff[i] {
			diff = append(diff, i)
		}
	}
	// 10 and 11 are the checksum, 52 is the low byte of the value.
	if want := []int{10, 11, 52}; !slices.Equal(diff, want) {
		t.Fatalf("set on/off differ at %v, want %v", diff, want)
	}
}

func TestFrameReturnsCopy(t *testing.T) {
	f := SetOn.frame()
	f[0] = 0
	if frameSetOn[0] != 0xa5 {
		t.Fatal("changing a returned frame changed the captured frame")
	}
}

func TestUnknownRequestHasNoFrame(t *testing.T) {
	for _, r := range []Request{0, 4, -1} {
		if f := r.frame(); f != nil {
			t.Errorf("%s: frame % x, want nil", r, f)
		}
	}
}

func TestCheckOutgoingAllowsCapturedFrames(t *testing.T) {
	for _, r := range []Request{GetDolby, SetOn, SetOff} {
		if err := CheckOutgoing(r.frame()); err != nil {
			t.Errorf("%s: %v", r, err)
		}
	}
}

func TestCheckOutgoingRefusesEverythingElse(t *testing.T) {
	refuse := func(name string, p []byte) {
		t.Helper()
		if CheckOutgoing(p) == nil {
			t.Errorf("%s: allowed % x", name, p)
		}
	}
	refuse("nil", nil)
	refuse("empty", []byte{})
	for _, r := range []Request{GetDolby, SetOn, SetOff} {
		f := r.frame()
		refuse(r.String()+" minus last byte", f[:len(f)-1])
		refuse(r.String()+" plus a byte", append(r.frame(), 0))
		refuse(r.String()+" twice", append(r.frame(), f...))
		for i := range f {
			for _, mask := range []byte{0x01, 0x80, 0xff} {
				g := r.frame()
				g[i] ^= mask
				refuse(r.String()+" with a changed byte", g)
			}
		}
	}
	refuse("a reply", replySet)
}

func TestMustAllowPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustAllow did not panic")
		}
	}()
	MustAllow([]byte{0xa5, 0x5a})
}

func TestCapturedReplies(t *testing.T) {
	m, err := Decode(replySet)
	if err != nil || !m.IsDolbyReply(OpSet) {
		t.Fatalf("set reply: %+v, %v", m, err)
	}
	for _, tc := range []struct {
		frame []byte
		want  bool
	}{{replyGetOn, true}, {replyGetOff, false}} {
		m, err := Decode(tc.frame)
		if err != nil || !m.IsDolbyReply(OpGet) {
			t.Fatalf("get reply: %+v, %v", m, err)
		}
		if on, err := m.DolbyState(); err != nil || on != tc.want {
			t.Errorf("DolbyState = %v, %v; want %v", on, err, tc.want)
		}
	}
}

func TestDolbyStateRejectsUnknownValues(t *testing.T) {
	for _, v := range [][]byte{nil, {0, 2, 1, 0}, {0, 1, 1}, {0, 1, 1, 0, 0}} {
		if _, err := (Message{Value: v}).DolbyState(); err == nil {
			t.Errorf("value % x accepted", v)
		}
	}
}

func TestNextSplitsStream(t *testing.T) {
	stream := append(append([]byte{0x00, 0xa5}, replyGetOn...), replySet[:20]...)
	f, n, err := Next(stream)
	if err != nil || !bytes.Equal(f, replyGetOn) || n != 2+len(replyGetOn) {
		t.Fatalf("first frame: n=%d err=%v", n, err)
	}
	if _, _, err := Next(stream[n:]); !errors.Is(err, ErrShort) {
		t.Fatalf("partial frame: err=%v, want ErrShort", err)
	}
}

func TestNextKeepsPossibleMagicStart(t *testing.T) {
	_, n, err := Next([]byte{1, 2, 0xa5})
	if !errors.Is(err, ErrShort) || n != 2 {
		t.Fatalf("n=%d err=%v; want 2 bytes dropped and the a5 kept", n, err)
	}
	_, n, _ = Next([]byte{1, 2, 3})
	if n != 3 {
		t.Fatalf("n=%d; want all junk dropped", n)
	}
}

func TestDecodeRejectsBadFrames(t *testing.T) {
	badCRC := bytes.Clone(replyGetOn)
	badCRC[len(badCRC)-3] ^= 1
	badLen := bytes.Clone(replyGetOn)
	badLen[8]++
	for name, f := range map[string][]byte{
		"checksum":    badCRC,
		"body length": badLen,
		"magic":       append([]byte{0xa5, 0x00}, replyGetOn[2:]...),
		"short":       replyGetOn[:11],
	} {
		if _, err := Decode(f); err == nil {
			t.Errorf("bad %s accepted", name)
		}
	}
}
