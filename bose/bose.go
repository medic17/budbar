// Package bose reads the listening mode of Bose QC Ultra Earbuds (2nd Gen)
// over BMAP, the protocol the Bose app speaks over RFCOMM. The protocol notes
// come from aaronsb/bosectl (https://github.com/aaronsb/bosectl, MIT).
//
// Packet layout:
//
//	block | function | flags | payload len | payload
//
// The low 4 bits of flags are the operator; the upper bits carry a device
// and port number, which this package ignores.
package bose

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Operators, from bosectl's notes.
const (
	OpSet        = 0
	OpGet        = 1
	OpSetGet     = 2
	OpStatus     = 3
	OpError      = 4
	OpStart      = 5
	OpResult     = 6
	OpProcessing = 7
)

// Functions of the AudioModes block (31).
const (
	blockAudioModes = 31
	funcGetAll      = 1
	funcCurrentMode = 3
	funcModeConfig  = 6
)

// presetModes are the firmware's fixed modes, by index. Indices from 4 up are
// slots for modes made in the Bose app (the earbuds come with "Work" in
// slot 4); their names are stored on the earbuds.
var presetModes = []string{"Quiet", "Aware", "Immersion", "Cinema"}

// presetName returns the name of the preset mode at idx, if it is one.
func presetName(idx int) (string, bool) {
	if idx >= 0 && idx < len(presetModes) {
		return presetModes[idx], true
	}
	return "", false
}

// Layout of a ModeConfig STATUS payload, as far as this package reads it:
// the mode index, then after 5 more bytes a 32-byte name ended by a zero
// byte.
const (
	modeNameStart = 6
	modeNameEnd   = 38
)

// modeConfigName reads the name from a ModeConfig STATUS payload.
func modeConfigName(payload []byte) (string, error) {
	if len(payload) < modeNameEnd {
		return "", fmt.Errorf("mode config too short (%d bytes): % x", len(payload), payload)
	}
	name, _, _ := bytes.Cut(payload[modeNameStart:modeNameEnd], []byte{0})
	if len(name) == 0 {
		return "", fmt.Errorf("mode %d has no name", payload[0])
	}
	return strings.ToValidUTF8(string(name), "?"), nil
}

// Request names one of the packets the earbuds may be sent.
type Request int

const (
	GetCurrentMode Request = iota + 1
	GetAllModes
)

func (r Request) String() string {
	switch r {
	case GetCurrentMode:
		return "get current mode"
	case GetAllModes:
		return "get all modes"
	}
	return fmt.Sprintf("Request(%d)", int(r))
}

// The only packets the earbuds may be sent, both without payload:
//   - a GET of AudioModes.CurrentMode;
//   - a START of AudioModes.GetAll, which makes the earbuds report every
//     mode's settings. START is also the operator that switches modes on
//     other functions, so only this exact packet is allowed.
var (
	packetGetCurrentMode = []byte{blockAudioModes, funcCurrentMode, OpGet, 0}
	packetGetAllModes    = []byte{blockAudioModes, funcGetAll, OpStart, 0}
)

// packet returns a copy of r's packet, or nil if r is not a known request.
func (r Request) packet() []byte {
	switch r {
	case GetCurrentMode:
		return bytes.Clone(packetGetCurrentMode)
	case GetAllModes:
		return bytes.Clone(packetGetAllModes)
	}
	return nil
}

// CheckOutgoing returns an error unless p is exactly one of the allowed
// packets, none of which changes a setting.
func CheckOutgoing(p []byte) error {
	for _, ok := range [][]byte{packetGetCurrentMode, packetGetAllModes} {
		if bytes.Equal(p, ok) {
			return nil
		}
	}
	return fmt.Errorf("refusing to send % x: not an allowed read request", p)
}

// MustAllow panics unless p is exactly one of the allowed packets. Every
// write to the earbuds goes through it.
func MustAllow(p []byte) {
	if err := CheckOutgoing(p); err != nil {
		panic(err)
	}
}

// Packet is one decoded BMAP packet.
type Packet struct {
	Block, Func, Op byte
	Payload         []byte
}

func (p Packet) String() string {
	return fmt.Sprintf("[%d.%d] op %d: % x", p.Block, p.Func, p.Op, p.Payload)
}

const headerLen = 4

// ErrShort means buf does not yet hold a whole packet.
var ErrShort = errors.New("incomplete packet")

// Next decodes the first packet in buf and returns it with the number of
// bytes it took. BMAP has no magic bytes or checksum to resynchronize on, so
// an unknown operator is an error rather than junk to skip.
func Next(buf []byte) (p Packet, n int, err error) {
	if len(buf) < headerLen {
		return p, 0, ErrShort
	}
	op := buf[2] & 0x0F
	if op > OpProcessing {
		return p, 0, fmt.Errorf("unknown operator %d in % x", op, buf[:headerLen])
	}
	n = headerLen + int(buf[3])
	if len(buf) < n {
		return p, 0, ErrShort
	}
	return Packet{Block: buf[0], Func: buf[1], Op: op, Payload: bytes.Clone(buf[headerLen:n])}, n, nil
}
