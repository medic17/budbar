package main

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"openfit-dolby/devices"
	"openfit-dolby/devices/devicestest"
)

func TestConnectErrorHintsAtShokzApp(t *testing.T) {
	err := connectError(windows.WSAEHOSTDOWN)
	if !strings.Contains(err.Error(), "Shokz app") || !errors.Is(err, windows.WSAEHOSTDOWN) {
		t.Errorf("connectError(WSAEHOSTDOWN) = %q", err)
	}
}

// withGuard sets the store dialRFCOMM checks and records every connection
// check, answering with connected and err.
func withGuard(t *testing.T, s devices.Store, connected bool, err error) *[]uint64 {
	var checked []uint64
	oldStore, oldCheck := targetStore, connectedCheck
	t.Cleanup(func() { targetStore, connectedCheck = oldStore, oldCheck })
	targetStore = s
	connectedCheck = func(addr uint64) (bool, error) { checked = append(checked, addr); return connected, err }
	return &checked
}

// The device guard runs first: anything but an added Shokz device is refused
// before the connection check or any socket.
func TestDialRefusesOtherDevices(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired, devicestest.BoseBudsDevice)
	checked := withGuard(t, s, true, nil)
	for name, addr := range map[string]uint64{
		"Shokz not added":  devicestest.ShokzDevice.Addr,
		"a Bose device":    devicestest.BoseBudsDevice.Addr,
		"untested earbuds": devicestest.JBLTunePaired.Addr,
		"unknown address":  0,
	} {
		if _, err := dialRFCOMM(addr); err == nil || errors.Is(err, errNotConnected) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if len(*checked) != 0 {
		t.Errorf("checked the connection of %X before refusing", *checked)
	}
}

func TestDialRefusesWhenNotConnected(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired, devicestest.ShokzDevice)
	addr := devicestest.ShokzDevice.Addr
	checked := withGuard(t, s, false, nil)
	if _, err := dialRFCOMM(addr); !errors.Is(err, errNotConnected) || len(*checked) != 1 || (*checked)[0] != addr {
		t.Errorf("err = %v, checked %X; want errNotConnected after checking %X", err, *checked, addr)
	}
	withGuard(t, s, false, errors.New("no radio"))
	if _, err := dialRFCOMM(addr); err == nil || !strings.Contains(err.Error(), "no radio") {
		t.Errorf("err = %v, want the check's error", err)
	}
}
func TestConnectErrorOtherErrorsHaveNoHint(t *testing.T) {
	err := connectError(windows.WSAETIMEDOUT)
	if strings.Contains(err.Error(), "Shokz app") || !errors.Is(err, windows.WSAETIMEDOUT) {
		t.Errorf("connectError(WSAETIMEDOUT) = %q", err)
	}
}
