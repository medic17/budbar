package bose

import (
	"errors"
	"testing"

	"openfit-dolby/bt"
	"openfit-dolby/devices"
	"openfit-dolby/devices/devicestest"
)

// withGuard sets the store Dial checks and records every connection check.
func withGuard(t *testing.T, s devices.Store) *[]uint64 {
	var checked []uint64
	oldStore, oldCheck := targetStore, connectedCheck
	t.Cleanup(func() { targetStore, connectedCheck = oldStore, oldCheck })
	targetStore = s
	connectedCheck = func(addr uint64) (bool, error) { checked = append(checked, addr); return false, nil }
	return &checked
}

// The device guard runs first: a device that is not an added Bose device is
// refused before the connection check or any socket.
func TestDialRefusesOtherDevices(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired, devicestest.ShokzDevice, devicestest.BoseBudsDevice)
	checked := withGuard(t, s)
	for name, addr := range map[string]uint64{
		"not added":       devicestest.BoseHPPaired.Addr,
		"a Shokz device":  devicestest.ShokzDevice.Addr,
		"untested device": devicestest.JBLTunePaired.Addr,
		"unknown address": 0,
	} {
		if _, err := Dial(addr); err == nil || errors.Is(err, ErrNotConnected) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if len(*checked) != 0 {
		t.Errorf("checked the connection of %X before refusing", *checked)
	}
}

// A listed device that Windows no longer identifies as its model is refused.
func TestDialRefusesWhenWindowsDisagrees(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired, devicestest.BoseBudsDevice)
	other := devicestest.BoseBudsPaired
	other.ProductID = 0x4024
	s.Paired = func() ([]bt.PairedDevice, error) { return []bt.PairedDevice{other}, nil }
	checked := withGuard(t, s)
	if _, err := Dial(other.Addr); !errors.Is(err, devices.ErrNotIdentified) || len(*checked) != 0 {
		t.Errorf("err = %v, checked %X", err, *checked)
	}
}

// Each added device's connection is checked by its own address.
func TestDialRefusesWhenNotConnected(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired, devicestest.BoseBudsDevice, devicestest.BoseHPDevice)
	checked := withGuard(t, s)
	for _, d := range []devices.Device{devicestest.BoseBudsDevice, devicestest.BoseHPDevice} {
		*checked = nil
		if _, err := Dial(d.Addr); !errors.Is(err, ErrNotConnected) || len(*checked) != 1 || (*checked)[0] != d.Addr {
			t.Errorf("%v: err = %v, checked %X", d, err, *checked)
		}
	}
}
