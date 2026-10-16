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
	p, err := c.request(GetCurrentMode, func(p Packet) bool {
		return p.Block == blockAudioModes && p.Func == funcCurrentMode && p.Op == OpStatus
	})
	if err != nil {
		return 0, err
	}
	if len(p.Payload) < 1 {
		return 0, fmt.Errorf("%s: empty reply", GetCurrentMode)
	}
	return int(p.Payload[0]), nil
}

// ModeName returns the name of the mode at index idx. Presets have fixed
// names; for other modes it asks the earbuds for every mode's settings and
// reads the name stored for idx.
func (c *Client) ModeName(idx int) (string, error) {
	if name, ok := presetName(idx); ok {
		return name, nil
	}
	p, err := c.request(GetAllModes, func(p Packet) bool {
		return p.Block == blockAudioModes && p.Func == funcModeConfig && p.Op == OpStatus &&
			len(p.Payload) > 0 && int(p.Payload[0]) == idx
	})
	if err != nil {
		return "", err
	}
	return modeConfigName(p.Payload)
}

// request sends r and returns the first packet that want accepts, skipping
// any others. An ERROR from the function r addressed ends the wait.
func (c *Client) request(r Request, want func(Packet) bool) (Packet, error) {
	sent := r.packet()
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
		if want(p) {
			return p, nil
		}
		if p.Op == OpError && p.Block == sent[0] && p.Func == sent[1] {
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
