package bose

import (
	"errors"
	"fmt"
	"io"
)

// ErrNoReply means the earbuds sent nothing before the read timeout.
var ErrNoReply = errors.New("no reply from earbuds")

const (
	readSize = 512
	// maxPerRequest caps how many bytes a request reads while waiting for
	// its reply, so a chatty or broken stream cannot keep it waiting forever.
	maxPerRequest = 16 << 10
)

// Client reads the earbuds' state over an open RFCOMM stream.
type Client struct {
	rw  io.ReadWriter
	buf []byte
}

func NewClient(rw io.ReadWriter) *Client {
	return &Client{rw: rw}
}

// CurrentMode returns the index of the active listening mode.
func (c *Client) CurrentMode() (int, error) {
	p, err := c.request(GetCurrentMode, blockAudioModes, funcCurrentMode)
	if err != nil {
		return 0, err
	}
	if len(p.Payload) < 1 {
		return 0, fmt.Errorf("%s: empty reply", GetCurrentMode)
	}
	return int(p.Payload[0]), nil
}

// request sends r and returns the earbuds' STATUS reply from block.fn,
// skipping any other packets they send in between.
func (c *Client) request(r Request, block, fn byte) (Packet, error) {
	if err := c.send(r); err != nil {
		return Packet{}, fmt.Errorf("send %s: %w", r, err)
	}
	read := 0
	for {
		p, n, err := Next(c.buf)
		if errors.Is(err, ErrShort) {
			if read >= maxPerRequest {
				return Packet{}, fmt.Errorf("no reply to %s within %d bytes", r, maxPerRequest)
			}
			got, err := c.fill()
			if err != nil {
				return Packet{}, err
			}
			read += got
			continue
		}
		if err != nil {
			return Packet{}, err
		}
		c.buf = c.buf[n:]
		if p.Block != block || p.Func != fn {
			continue
		}
		switch p.Op {
		case OpStatus:
			return p, nil
		case OpError:
			return Packet{}, fmt.Errorf("%s: earbuds answered with error % x", r, p.Payload)
		}
	}
}

func (c *Client) send(r Request) error {
	p := r.packet()
	MustAllow(p)
	_, err := c.rw.Write(p)
	return err
}

func (c *Client) fill() (int, error) {
	var tmp [readSize]byte
	n, err := c.rw.Read(tmp[:])
	c.buf = append(c.buf, tmp[:n]...)
	switch {
	case n > 0:
		return n, nil
	case err == nil, errors.Is(err, io.EOF):
		return 0, errors.New("earbuds closed the connection")
	}
	return 0, err
}
