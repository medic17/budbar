package bose

import (
	"errors"
	"fmt"
	"io"

	"openfit-dolby/bt"
)

// BudsAddr is the Bluetooth address of the one pair of earbuds this package
// was tested with (Bose QC Ultra Earbuds, 2nd Gen). Dial connects to no
// other address.
const BudsAddr uint64 = 0x68F21F3B5CB7

// bmapChannel is the RFCOMM channel that speaks BMAP. The earbuds' SDP
// records list two Serial Port services (channels 1 and 2), so a lookup by
// service class could pick either; the 00000000-deca-fade-... service is a
// status beacon on channel 14.
const bmapChannel = 2

// ErrNotConnected means the earbuds are not connected to this PC.
var ErrNotConnected = errors.New("Bose earbuds not connected to this PC")

// connectedCheck reports whether the earbuds are connected; tests replace it.
var connectedCheck = Connected

// Connected asks Windows whether the earbuds are connected to this PC. It
// only reads Windows' own records; nothing is sent over the air.
func Connected() (bool, error) { return bt.Connected(BudsAddr) }

// checkTarget returns an error unless addr is the tested earbuds' address.
func checkTarget(addr uint64) error {
	if addr != BudsAddr {
		return fmt.Errorf("refusing to connect to %012X: only %012X has been tested", addr, BudsAddr)
	}
	return nil
}

// Dial connects to the BMAP channel of the earbuds at addr. It panics for any
// address but BudsAddr. It connects only while the earbuds are connected to
// this PC: an RFCOMM connect to a disconnected device makes Windows page it,
// which could pull the earbuds away from the phone. The stream sends only the
// requests CheckOutgoing allows.
func Dial(addr uint64) (io.ReadWriteCloser, error) {
	if err := checkTarget(addr); err != nil {
		panic(err)
	}
	ok, err := connectedCheck()
	if err != nil {
		return nil, fmt.Errorf("checking the earbuds' connection: %w", err)
	}
	if !ok {
		return nil, ErrNotConnected
	}
	c, err := bt.Dial(bt.Config{
		Addr:       BudsAddr,
		Port:       bmapChannel,
		Allow:      MustAllow,
		ErrNoReply: ErrNoReply,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
