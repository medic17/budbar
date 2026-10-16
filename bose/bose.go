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

// AudioModes.CurrentMode: block 31, function 3.
const (
	blockAudioModes = 31
	funcCurrentMode = 3
)

// presetModes are the firmware's fixed modes, by index. Indices from 4 up are
// slots for modes made in the Bose app (the earbuds come with "Work" in
// slot 4); their names are stored on the earbuds.
var presetModes = []string{"Quiet", "Aware", "Immersion", "Cinema"}

// ModeName names the mode at index idx: a preset's name, or "custom mode N".
func ModeName(idx int) string {
	if idx >= 0 && idx < len(presetModes) {
		return presetModes[idx]
	}
	return fmt.Sprintf("custom mode %d", idx)
}

// Request names one of the packets the earbuds may be sent.
type Request int

const (
	GetCurrentMode Request = iota + 1
)

func (r Request) String() string {
	switch r {
	case GetCurrentMode:
		return "get current mode"
	}
	return fmt.Sprintf("Request(%d)", int(r))
}

// packetGetCurrentMode is a GET of AudioModes.CurrentMode with no payload.
var packetGetCurrentMode = []byte{blockAudioModes, funcCurrentMode, OpGet, 0}

// packet returns a copy of r's packet, or nil if r is not a known request.
func (r Request) packet() []byte {
	switch r {
	case GetCurrentMode:
		return bytes.Clone(packetGetCurrentMode)
	}
	return nil
}

// CheckOutgoing returns an error unless p is exactly one of the allowed
// packets. All of them are GETs, so nothing sent can change a setting.
func CheckOutgoing(p []byte) error {
	if bytes.Equal(p, packetGetCurrentMode) {
		return nil
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
