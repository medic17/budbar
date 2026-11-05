package bose

import (
	"errors"
	"fmt"
	"io"

	"openfit-dolby/bt"
	"openfit-dolby/devices"
)

// bmapChannel is the RFCOMM channel that speaks BMAP on both devices. The
// earbuds' SDP records list two Serial Port services (channels 1 and 2) and
// the headphones' one (channel 2), so a lookup by service class could pick
// the wrong one; the 00000000-deca-fade-... service is a status beacon on
// channel 14 on both.
const bmapChannel = 2

// ErrNotConnected means the device is not connected to this PC.
var ErrNotConnected = errors.New("Bose device not connected to this PC")

// targetStore and connectedCheck guard Dial; tests replace them.
var (
	targetStore    = devices.Default()
	connectedCheck = Connected
)

// Connected asks Windows whether the device at addr is connected to this PC.
// It only reads Windows' own records; nothing is sent over the air.
func Connected(addr uint64) (bool, error) { return bt.Connected(addr) }

// Dial connects to the BMAP channel of the device at addr. It refuses unless
// addr is an added Bose device that Windows' records still identify as its
// model. It connects only while the device is connected to this PC: an
// RFCOMM connect to a disconnected device makes Windows page it, which could
// pull the device away from the phone. The stream sends only the requests
// CheckOutgoing allows.
func Dial(addr uint64) (io.ReadWriteCloser, error) {
	if _, err := targetStore.Verify(addr, devices.Bose); err != nil {
		return nil, err
	}
	ok, err := connectedCheck(addr)
	if err != nil {
		return nil, fmt.Errorf("checking the device's connection: %w", err)
	}
	if !ok {
		return nil, ErrNotConnected
	}
	c, err := bt.Dial(bt.Config{
		Addr:       addr,
		Port:       bmapChannel,
		Allow:      MustAllow,
		ErrNoReply: ErrNoReply,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
