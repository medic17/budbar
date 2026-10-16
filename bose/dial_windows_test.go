package bose

import (
	"errors"
	"testing"
)

func TestCheckTarget(t *testing.T) {
	if err := checkTarget(BudsAddr); err != nil {
		t.Errorf("tested earbuds refused: %v", err)
	}
	for _, addr := range []uint64{0, BudsAddr ^ 1, 0xA00CE275632F, 0x68F21F0DF042} {
		if checkTarget(addr) == nil {
			t.Errorf("%012X allowed", addr)
		}
	}
}

// The address guard runs first: another address panics before the
// connection check or any socket.
func TestDialPanicsForOtherAddress(t *testing.T) {
	checked := false
	defer func(old func() (bool, error)) { connectedCheck = old }(connectedCheck)
	connectedCheck = func() (bool, error) { checked = true; return true, nil }
	defer func() {
		if recover() == nil {
			t.Error("Dial did not panic")
		}
		if checked {
			t.Error("checked the connection before refusing the address")
		}
	}()
	Dial(BudsAddr ^ 1)
}

func TestDialRefusesWhenNotConnected(t *testing.T) {
	defer func(old func() (bool, error)) { connectedCheck = old }(connectedCheck)
	connectedCheck = func() (bool, error) { return false, nil }
	if _, err := Dial(BudsAddr); !errors.Is(err, ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}
