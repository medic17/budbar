package main

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConnectErrorHintsAtShokzApp(t *testing.T) {
	err := connectError(windows.WSAEHOSTDOWN)
	if !strings.Contains(err.Error(), "Shokz app") || !errors.Is(err, windows.WSAEHOSTDOWN) {
		t.Errorf("connectError(WSAEHOSTDOWN) = %q", err)
	}
}

func TestCheckTarget(t *testing.T) {
	if err := checkTarget(earbudsAddr); err != nil {
		t.Errorf("tested earbuds refused: %v", err)
	}
	for _, addr := range []uint64{0, earbudsAddr ^ 1, earbudsAddr ^ 1<<47, 0xFFFFFFFFFFFF, 0x2F6375E20CA0} {
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
			t.Error("dialRFCOMM did not panic")
		}
		if checked {
			t.Error("checked the connection before refusing the address")
		}
	}()
	dialRFCOMM(earbudsAddr ^ 1)
}

func TestDialRefusesWhenNotConnected(t *testing.T) {
	defer func(old func() (bool, error)) { connectedCheck = old }(connectedCheck)
	connectedCheck = func() (bool, error) { return false, nil }
	if _, err := dialRFCOMM(earbudsAddr); !errors.Is(err, errNotConnected) {
		t.Errorf("err = %v, want errNotConnected", err)
	}
	connectedCheck = func() (bool, error) { return false, errors.New("no radio") }
	if _, err := dialRFCOMM(earbudsAddr); err == nil || !strings.Contains(err.Error(), "no radio") {
		t.Errorf("err = %v, want the check's error", err)
	}
}

func TestConnectErrorOtherErrorsHaveNoHint(t *testing.T) {
	err := connectError(windows.WSAETIMEDOUT)
	if strings.Contains(err.Error(), "Shokz app") || !errors.Is(err, windows.WSAETIMEDOUT) {
		t.Errorf("connectError(WSAETIMEDOUT) = %q", err)
	}
}
