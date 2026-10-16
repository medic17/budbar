package main

import (
	"errors"
	"fmt"
	"io"

	"openfit-dolby/bose"
	"openfit-dolby/bt"
)

// bmapChannel is the RFCOMM channel that speaks BMAP. The earbuds' SDP
// records list two Serial Port services (channels 1 and 2), so a lookup by
// service class could pick either; the 00000000-deca-fade-... service is a
// status beacon on channel 14.
const bmapChannel = 2

// errNotConnected means the earbuds are not connected to this PC.
var errNotConnected = errors.New("earbuds not connected to this PC")

// connectedCheck reports whether the earbuds are connected; tests replace it.
var connectedCheck = func() (bool, error) { return bt.Connected(budsAddr) }

// checkTarget returns an error unless addr is the tested earbuds' address.
func checkTarget(addr uint64) error {
	if addr != budsAddr {
		return fmt.Errorf("refusing to connect to %012X: only %012X has been tested", addr, budsAddr)
	}
	return nil
}

// dialRFCOMM connects to the earbuds' BMAP service. It panics for any other
// address. It connects only while the earbuds are connected to this PC, and
// the stream sends only the read requests bose allows.
func dialRFCOMM(addr uint64) (io.ReadWriteCloser, error) {
	if err := checkTarget(addr); err != nil {
		panic(err)
	}
	ok, err := connectedCheck()
	if err != nil {
		return nil, fmt.Errorf("checking the earbuds' connection: %w", err)
	}
	if !ok {
		return nil, errNotConnected
	}
	c, err := bt.Dial(bt.Config{
		Addr:       budsAddr,
		Port:       bmapChannel,
		Allow:      bose.MustAllow,
		ErrNoReply: bose.ErrNoReply,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
