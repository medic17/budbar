package shokz

import (
	"bytes"
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

// Client reads and switches Dolby Audio over an open RFCOMM stream.
type Client struct {
	rw      io.ReadWriter
	buf     []byte
	setSent bool
}

func NewClient(rw io.ReadWriter) *Client {
	return &Client{rw: rw}
}

// Status asks the earbuds whether Dolby Audio is on.
func (c *Client) Status() (on bool, err error) {
	m, err := c.request(GetDolby, OpGet)
	if err != nil {
		return false, err
	}
	return m.DolbyState()
}

// Set switches Dolby Audio on or off and reports whether it sent a set
// request. It reads the state first and sends nothing when it already
// matches, then reads it again to confirm the change. A Client sends at most
// one set request.
func (c *Client) Set(on bool) (sent bool, err error) {
	cur, err := c.Status()
	if err != nil {
		return false, err
	}
	if cur == on {
		return false, nil
	}
	return c.change(on)
}

// Toggle switches Dolby Audio to the opposite of what the earbuds report and
// returns the new state. Like Set, it confirms the change by reading again.
func (c *Client) Toggle() (on bool, err error) {
	cur, err := c.Status()
	if err != nil {
		return false, err
	}
	if _, err := c.change(!cur); err != nil {
		return false, err
	}
	return !cur, nil
}

// change sends the one set request this Client may send and confirms the
// earbuds now report on.
func (c *Client) change(on bool) (sent bool, err error) {
	if c.setSent {
		return false, errors.New("this connection already sent a set request")
	}
	req := SetOff
	if on {
		req = SetOn
	}
	c.setSent = true
	m, err := c.request(req, OpSet)
	if err != nil {
		return true, fmt.Errorf("%s: %w", req, err)
	}
	if !bytes.Equal(m.Value, []byte{0, 0, 0, 0}) {
		return true, fmt.Errorf("%s: earbuds answered % x instead of 00 00 00 00", req, m.Value)
	}
	now, err := c.Status()
	if err != nil {
		return true, fmt.Errorf("confirm: %w", err)
	}
	if now != on {
		return true, errors.New("earbuds accepted the set request but still report the old state")
	}
	return true, nil
}

// request sends r and returns the earbuds' Dolby reply to op, skipping any
// other frames they send in between.
func (c *Client) request(r Request, op uint32) (Message, error) {
	if err := c.send(r); err != nil {
		return Message{}, fmt.Errorf("send %s: %w", r, err)
	}
	read := 0
	for {
		f, n, err := Next(c.buf)
		c.buf = c.buf[n:]
		if errors.Is(err, ErrShort) {
			if read >= maxPerRequest {
				return Message{}, fmt.Errorf("no reply to %s within %d bytes", r, maxPerRequest)
			}
			got, err := c.fill()
			if err != nil {
				return Message{}, err
			}
			read += got
			continue
		}
		m, err := Decode(f)
		if err != nil {
			return Message{}, err
		}
		if m.IsDolbyReply(op) {
			return m, nil
		}
	}
}

func (c *Client) send(r Request) error {
	p := r.frame()
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
