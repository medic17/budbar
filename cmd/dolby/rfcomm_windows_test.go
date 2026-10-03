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

func TestConnectErrorOtherErrorsHaveNoHint(t *testing.T) {
	err := connectError(windows.WSAETIMEDOUT)
	if strings.Contains(err.Error(), "Shokz app") || !errors.Is(err, windows.WSAETIMEDOUT) {
		t.Errorf("connectError(WSAETIMEDOUT) = %q", err)
	}
}
