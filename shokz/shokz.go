// Package shokz encodes and decodes the Shokz app's RFCOMM messages for the
// Dolby Audio setting, as captured from the Android Shokz app.
//
// Frame layout (all integers little-endian):
//
//	a5 5a | outer len (2) | 01 | dir (00 request, 01 response) | 00 00 |
//	body len (2) | CRC-16 of body (2) | body
//
// outer len = body len + 4. The body is 01 00 00 00, the body length minus 8
// as a uint32, then type/length/value fields with uint32 type and length.
package shokz

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Request names one of the frames the earbuds may be sent.
type Request int

const (
	GetDolby Request = iota + 1
	SetOn
	SetOff
)

func (r Request) String() string {
	switch r {
	case GetDolby:
		return "get"
	case SetOn:
		return "set on"
	case SetOff:
		return "set off"
	}
	return fmt.Sprintf("Request(%d)", int(r))
}

// Frames captured from the Shokz app. They are sent byte for byte; nothing
// in this package builds new frames to send.
var (
	frameSetOff = mustHex("a5 5a 30 00 01 00 00 00 2c 00 34 42 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 01 00 00 00 03 00 00 00 04 00 00 00 2b 00 00 00 04 00 00 00 04 00 00 00 00 00 00 00")
	frameSetOn = mustHex("a5 5a 30 00 01 00 00 00 2c 00 35 be 01 00 00 00 24 00 00 00 02 00 00 00 " +
		"04 00 00 00 01 00 00 00 03 00 00 00 04 00 00 00 2b 00 00 00 04 00 00 00 04 00 00 00 01 00 00 00")
	frameGetDolby = mustHex("a5 5a 2c 00 01 00 00 00 28 00 c7 14 01 00 00 00 20 00 00 00 02 00 00 00 " +
		"04 00 00 00 02 00 00 00 03 00 00 00 04 00 00 00 2b 00 00 00 04 00 00 00 00 00 00 00")
)

// frame returns a copy of r's captured frame, or nil if r is not a known
// request.
func (r Request) frame() []byte {
	switch r {
	case GetDolby:
		return bytes.Clone(frameGetDolby)
	case SetOn:
		return bytes.Clone(frameSetOn)
	case SetOff:
		return bytes.Clone(frameSetOff)
	}
	return nil
}

// CheckOutgoing returns an error unless p is exactly one of the captured
// frames.
func CheckOutgoing(p []byte) error {
	for _, f := range [][]byte{frameGetDolby, frameSetOn, frameSetOff} {
		if bytes.Equal(p, f) {
			return nil
		}
	}
	return fmt.Errorf("refusing to send % x: not a captured frame", p)
}

// MustAllow panics unless p is exactly one of the captured frames. Every
// write to the earbuds goes through it.
func MustAllow(p []byte) {
	if err := CheckOutgoing(p); err != nil {
		panic(err)
	}
}

const (
	headerLen = 12

	tlvOp      = 2
	tlvFeature = 3
	tlvValue   = 4

	OpSet = 1
	OpGet = 2

	FeatureDolby    = 0x2b
	responseFeature = 0x8000
)

// Message is one decoded frame.
type Message struct {
	Response bool
	Op       uint32
	Feature  uint32
	Value    []byte
}

// CRC16 is CRC-16 with polynomial 0x8005 (reflected), init 0, xor-out 0xFFFF.
func CRC16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// ErrShort means buf does not yet hold a whole frame.
var ErrShort = errors.New("incomplete frame")

// Next finds the first frame in buf. It returns the frame and the number of
// bytes consumed, including any junk before the frame's magic. On ErrShort,
// the consumed bytes are junk that can be dropped.
func Next(buf []byte) (frame []byte, n int, err error) {
	i := bytes.Index(buf, []byte{0xa5, 0x5a})
	if i < 0 {
		// Keep a trailing a5: it may be the start of the next magic.
		if len(buf) > 0 && buf[len(buf)-1] == 0xa5 {
			return nil, len(buf) - 1, ErrShort
		}
		return nil, len(buf), ErrShort
	}
	buf = buf[i:]
	if len(buf) < headerLen {
		return nil, i, ErrShort
	}
	total := 8 + int(binary.LittleEndian.Uint16(buf[2:]))
	if len(buf) < total {
		return nil, i, ErrShort
	}
	return buf[:total], i + total, nil
}

// Decode checks a frame's lengths and checksum and reads its fields.
func Decode(f []byte) (Message, error) {
	var m Message
	if len(f) < headerLen || f[0] != 0xa5 || f[1] != 0x5a {
		return m, fmt.Errorf("not a frame: % x", f)
	}
	bodyLen := int(binary.LittleEndian.Uint16(f[8:]))
	body := f[headerLen:]
	if len(body) != bodyLen {
		return m, fmt.Errorf("body length %d, header says %d", len(body), bodyLen)
	}
	if got, want := CRC16(body), binary.LittleEndian.Uint16(f[10:]); got != want {
		return m, fmt.Errorf("checksum %04x, header says %04x", got, want)
	}
	m.Response = f[5] == 1
	if len(body) < 8 {
		return m, fmt.Errorf("body too short: % x", body)
	}
	for p := body[8:]; len(p) > 0; {
		if len(p) < 8 {
			return m, fmt.Errorf("truncated field: % x", p)
		}
		typ, n := binary.LittleEndian.Uint32(p), binary.LittleEndian.Uint32(p[4:])
		if uint32(len(p)-8) < n {
			return m, fmt.Errorf("field %d longer than body", typ)
		}
		val := p[8 : 8+n]
		switch typ {
		case tlvOp:
			m.Op = le32(val)
		case tlvFeature:
			m.Feature = le32(val)
		case tlvValue:
			m.Value = val
		}
		p = p[8+n:]
	}
	return m, nil
}

// IsDolbyReply reports whether m answers a Dolby request with operation op.
func (m Message) IsDolbyReply(op uint32) bool {
	return m.Response && m.Op == op && m.Feature == FeatureDolby|responseFeature
}

// DolbyState reads the on/off state from a get reply. The second value byte
// holds the state; the meaning of the other bytes is unknown.
func (m Message) DolbyState() (bool, error) {
	if len(m.Value) != 4 || m.Value[1] > 1 {
		return false, fmt.Errorf("unexpected Dolby value % x", m.Value)
	}
	return m.Value[1] == 1, nil
}

func le32(b []byte) uint32 {
	var v [4]byte
	copy(v[:], b)
	return binary.LittleEndian.Uint32(v[:])
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}
