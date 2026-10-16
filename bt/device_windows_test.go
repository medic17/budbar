package bt

import (
	"testing"
	"unsafe"
)

func TestDeviceInfoLayout(t *testing.T) {
	// Offsets and size of BLUETOOTH_DEVICE_INFO on 64-bit Windows.
	var d deviceInfo
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"Address", unsafe.Offsetof(d.Address), 8},
		{"fConnected", unsafe.Offsetof(d.Connected), 20},
		{"stLastSeen", unsafe.Offsetof(d.LastSeen), 32},
		{"szName", unsafe.Offsetof(d.Name), 64},
		{"sizeof", unsafe.Sizeof(d), 560},
	} {
		if tc.got != tc.want {
			t.Errorf("%s at %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
