// Package bt holds the Windows Bluetooth pieces the device tools share: an
// RFCOMM stream that checks every outgoing byte, the connection state Windows
// records for a device, and a tracer that logs the bytes on a stream.
package bt

import (
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/sys/windows"
)

const readTimeout = 5 * time.Second

// Config says which service on which device to connect to, and what may be
// sent there.
type Config struct {
	Addr uint64
	// Port is the RFCOMM channel. When it is 0, Windows finds the channel
	// from Service through SDP.
	Port uint32
	// Service is the SDP service class, used when Port is 0.
	Service windows.GUID
	// Allow panics unless p may be sent. Every write calls it first.
	Allow func(p []byte)
	// ErrNoReply is what Read returns when nothing arrives within the read
	// timeout.
	ErrNoReply error
}

// Conn is a blocking Windows Bluetooth RFCOMM socket.
type Conn struct {
	s   windows.Handle
	cfg Config
}

// Dial connects to cfg.Service on cfg.Addr. Callers check that the device is
// connected first: an RFCOMM connect to a disconnected device makes Windows
// page it, which could pull it away from the phone.
func Dial(cfg Config) (*Conn, error) {
	if cfg.Allow == nil || cfg.ErrNoReply == nil {
		panic("bt.Dial: Config needs Allow and ErrNoReply")
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
	c := &Conn{s: s, cfg: cfg}
	ms := int(readTimeout / time.Millisecond)
	if err := windows.SetsockoptInt(s, windows.SOL_SOCKET, windows.SO_RCVTIMEO, ms); err != nil {
		c.Close()
		return nil, fmt.Errorf("set timeout: %w", err)
	}
	sa := &windows.SockaddrBth{BtAddr: cfg.Addr, Port: cfg.Port}
	if cfg.Port == 0 {
		sa.ServiceClassId = cfg.Service
	}
	if err := windows.Connect(s, sa); err != nil {
		c.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	return c, nil
}

// Write sends p after cfg.Allow has accepted it.
func (c *Conn) Write(p []byte) (int, error) {
	c.cfg.Allow(p)
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

func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var got, flags uint32
	b := windows.WSABuf{Len: uint32(len(p)), Buf: &p[0]}
	if err := windows.WSARecv(c.s, &b, 1, &got, &flags, nil, nil); err != nil {
		if errors.Is(err, windows.WSAETIMEDOUT) {
			return 0, c.cfg.ErrNoReply
		}
		return 0, fmt.Errorf("receive: %w", err)
	}
	if got == 0 {
		return 0, io.EOF
	}
	return int(got), nil
}

func (c *Conn) Close() error {
	err := windows.Closesocket(c.s)
	windows.WSACleanup()
	return err
}
