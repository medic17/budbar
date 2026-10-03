package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/sys/windows"

	"openfit-dolby/shokz"
)

// shokzService is the SDP service class (16-bit UUID 0xFEF0) the Shokz app
// looks up to find the earbuds' RFCOMM channel.
var shokzService = windows.GUID{
	Data1: 0x0000FEF0, Data2: 0x0000, Data3: 0x1000,
	Data4: [8]byte{0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB},
}

const readTimeout = 5 * time.Second

// rfcomm is a blocking Windows Bluetooth RFCOMM socket.
type rfcomm struct {
	s windows.Handle
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
// device. It connects only while the earbuds are connected to this PC: an
// RFCOMM connect to a disconnected device makes Windows page it, which could
// pull the earbuds away from the phone.
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
	var wsa windows.WSAData
	if err := windows.WSAStartup(0x0202, &wsa); err != nil {
		return nil, fmt.Errorf("WSAStartup: %w", err)
	}
	s, err := windows.Socket(windows.AF_BTH, windows.SOCK_STREAM, windows.BTHPROTO_RFCOMM)
	if err != nil {
		windows.WSACleanup()
		return nil, fmt.Errorf("socket: %w", err)
	}
	c := &rfcomm{s: s}
	ms := int(readTimeout / time.Millisecond)
	if err := windows.SetsockoptInt(s, windows.SOL_SOCKET, windows.SO_RCVTIMEO, ms); err != nil {
		c.Close()
		return nil, fmt.Errorf("set timeout: %w", err)
	}
	// Port 0 with a service class makes Windows find the channel through SDP.
	if err := windows.Connect(s, &windows.SockaddrBth{BtAddr: earbudsAddr, ServiceClassId: shokzService}); err != nil {
		c.Close()
		return nil, connectError(err)
	}
	return c, nil
}

// connectError explains a failed connect. The earbuds refuse with
// WSAEHOSTDOWN while the Shokz app holds its connection.
func connectError(err error) error {
	if errors.Is(err, windows.WSAEHOSTDOWN) {
		return fmt.Errorf("connect: %w (is the Shokz app open on your phone? Close it and try again)", err)
	}
	return fmt.Errorf("connect: %w", err)
}

// Write sends p, which must be a captured frame.
func (c *rfcomm) Write(p []byte) (int, error) {
	shokz.MustAllow(p)
	total := 0
	for total < len(p) {
		var sent uint32
		b := windows.WSABuf{Len: uint32(len(p) - total), Buf: &p[total]}
		if err := windows.WSASend(c.s, &b, 1, &sent, 0, nil, nil); err != nil {
			return total, fmt.Errorf("send: %w", err)
		}
		total += int(sent)
	}
	return total, nil
}

func (c *rfcomm) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var got, flags uint32
	b := windows.WSABuf{Len: uint32(len(p)), Buf: &p[0]}
	if err := windows.WSARecv(c.s, &b, 1, &got, &flags, nil, nil); err != nil {
		if errors.Is(err, windows.WSAETIMEDOUT) {
			return 0, shokz.ErrNoReply
		}
		return 0, fmt.Errorf("receive: %w", err)
	}
	if got == 0 {
		return 0, io.EOF
	}
	return int(got), nil
}

func (c *rfcomm) Close() error {
	err := windows.Closesocket(c.s)
	windows.WSACleanup()
	return err
}
