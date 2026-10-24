package bose

import (
	"errors"
	"testing"
)

func TestCheckTarget(t *testing.T) {
	for _, d := range []Device{Earbuds, Headphones} {
		if err := checkTarget(d.Addr); err != nil {
			t.Errorf("%v refused: %v", d, err)
		}
	}
	for _, addr := range []uint64{0, Earbuds.Addr ^ 1, Headphones.Addr ^ 1, 0xA00CE275632F} {
		if checkTarget(addr) == nil {
			t.Errorf("%012X allowed", addr)
		}
	}
}

// The address guard runs first: another address panics before the
// connection check or any socket.
func TestDialPanicsForOtherAddress(t *testing.T) {
	checked := false
	defer func(old func(uint64) (bool, error)) { connectedCheck = old }(connectedCheck)
	connectedCheck = func(uint64) (bool, error) { checked = true; return true, nil }
	defer func() {
		if recover() == nil {
			t.Error("Dial did not panic")
		}
		if checked {
			t.Error("checked the connection before refusing the address")
		}
	}()
	Dial(Earbuds.Addr ^ 1)
}

// Each device's connection is checked by its own address.
func TestDialRefusesWhenNotConnected(t *testing.T) {
	defer func(old func(uint64) (bool, error)) { connectedCheck = old }(connectedCheck)
	for _, d := range []Device{Earbuds, Headphones} {
		var asked uint64
		connectedCheck = func(addr uint64) (bool, error) { asked = addr; return false, nil }
		if _, err := Dial(d.Addr); !errors.Is(err, ErrNotConnected) || asked != d.Addr {
			t.Errorf("%v: err = %v, checked %012X", d, err, asked)
		}
	}
}

func TestDeviceNamed(t *testing.T) {
	for _, d := range []Device{Earbuds, Headphones} {
		if got, ok := DeviceNamed(d.Name); !ok || got != d {
			t.Errorf("DeviceNamed(%q) = %v, %t", d.Name, got, ok)
		}
	}
	if _, ok := DeviceNamed("Earbuds"); ok {
		t.Error("names are matched case-insensitively")
	}
}
