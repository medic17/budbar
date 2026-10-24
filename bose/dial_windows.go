package bose

import (
	"errors"
	"fmt"
	"io"

	"openfit-dolby/bt"
)

// bmapChannel is the RFCOMM channel that speaks BMAP on both devices. The
// earbuds' SDP records list two Serial Port services (channels 1 and 2) and
// the headphones' one (channel 2), so a lookup by service class could pick
// the wrong one; the 00000000-deca-fade-... service is a status beacon on
// channel 14 on both.
const bmapChannel = 2

// ErrNotConnected means the device is not connected to this PC.
var ErrNotConnected = errors.New("Bose device not connected to this PC")

// connectedCheck reports whether the device at addr is connected; tests
// replace it.
var connectedCheck = Connected

// Connected asks Windows whether the device at addr is connected to this PC.
// It only reads Windows' own records; nothing is sent over the air.
func Connected(addr uint64) (bool, error) { return bt.Connected(addr) }

// checkTarget returns an error unless addr is one of Devices.
func checkTarget(addr uint64) error {
	for _, d := range Devices {
		if d.Addr == addr {
			return nil
		}
	}
	return fmt.Errorf("refusing to connect to %012X: not one of the known Bose devices", addr)
}

// Dial connects to the BMAP channel of the device at addr. It panics for any
// address not in Devices. It connects only while the device is connected to
// this PC: an RFCOMM connect to a disconnected device makes Windows page it,
// which could pull the device away from the phone. The stream sends only the
// requests CheckOutgoing allows.
func Dial(addr uint64) (io.ReadWriteCloser, error) {
	if err := checkTarget(addr); err != nil {
		panic(err)
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
