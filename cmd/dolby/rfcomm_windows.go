package main

import (
	"errors"
	"fmt"
	"io"

	"golang.org/x/sys/windows"

	"openfit-dolby/bt"
	"openfit-dolby/shokz"
)

// shokzService is the SDP service class (16-bit UUID 0xFEF0) the Shokz app
// looks up to find the earbuds' RFCOMM channel.
var shokzService = windows.GUID{
	Data1: 0x0000FEF0, Data2: 0x0000, Data3: 0x1000,
	Data4: [8]byte{0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB},
}

// errNotConnected means the earbuds are not connected to this PC.
var errNotConnected = errors.New("earbuds not connected to this PC")

// connectedCheck reports whether the earbuds are connected; tests replace it.
var connectedCheck = earbudsConnected

// checkTarget returns an error unless addr is the tested earbuds' address.
func checkTarget(addr uint64) error {
	if addr != earbudsAddr {
		return fmt.Errorf("refusing to connect to %012X: only %012X has been tested", addr, earbudsAddr)
	}
	return nil
}

// dialRFCOMM connects to the earbuds' Shokz service. It panics for any other
// address, so even a bug elsewhere cannot send the captured frames to another
// device. It connects only while the earbuds are connected to this PC, and
// the stream sends only the captured frames.
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
		Addr:       earbudsAddr,
		Service:    shokzService,
		Allow:      shokz.MustAllow,
		ErrNoReply: shokz.ErrNoReply,
	})
	if err != nil {
		return nil, connectError(err)
	}
	return c, nil
}

// connectError explains a failed connect. The earbuds refuse with
// WSAEHOSTDOWN while the Shokz app holds its connection.
func connectError(err error) error {
	if errors.Is(err, windows.WSAEHOSTDOWN) {
		return fmt.Errorf("%w (is the Shokz app open on your phone? Close it and try again)", err)
	}
	return err
}
