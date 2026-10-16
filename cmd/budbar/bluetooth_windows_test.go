package main

import (
	"testing"
	"unsafe"
)

func TestDevBroadcastHandleLayout(t *testing.T) {
	// Offsets of DEV_BROADCAST_HANDLE on 64-bit Windows.
	var h devBroadcastHandle
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"dbch_handle", unsafe.Offsetof(h.Handle), 16},
		{"dbch_eventguid", unsafe.Offsetof(h.EventGUID), 32},
		{"dbch_nameoffset", unsafe.Offsetof(h.NameOffset), 48},
		{"dbch_data", unsafe.Offsetof(h.Data), 52},
	} {
		if tc.got != tc.want {
			t.Errorf("%s at %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// broadcast builds a DEV_BROADCAST_HANDLE carrying data, as Windows would.
// The buffer is made of uint64s so it is aligned like Windows' own.
func broadcast(guidOK bool, data []byte) *devBroadcastHandle {
	var h devBroadcastHandle
	n := unsafe.Offsetof(h.Data) + uintptr(len(data)) + 8
	words := make([]uint64, (n+7)/8)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), n)
	p := (*devBroadcastHandle)(unsafe.Pointer(&words[0]))
	p.Size = uint32(unsafe.Offsetof(h.Data)) + uint32(len(data))
	p.DeviceType = dbtDevtypHandle
	if guidOK {
		p.EventGUID = guidBluetoothHCIEvent
	}
	copy(buf[unsafe.Offsetof(h.Data):], data)
	return p
}

func TestHCIEventFrom(t *testing.T) {
	info := []byte{0x2F, 0x63, 0x75, 0xE2, 0x0C, 0xA0, 0, 0, hciConnTypeACL, 1}
	ev, ok := hciEventFrom(broadcast(true, info))
	if !ok || ev != (hciEvent{Addr: 0xA00CE275632F, ConnType: hciConnTypeACL, Connected: true}) {
		t.Errorf("got %+v, %v", ev, ok)
	}
	if _, ok := hciEventFrom(broadcast(false, info)); ok {
		t.Error("accepted an event with another GUID")
	}
	if _, ok := hciEventFrom(broadcast(true, info[:9])); ok {
		t.Error("accepted a truncated event")
	}
}

func TestParseHCIEventDisconnect(t *testing.T) {
	ev, ok := parseHCIEvent([]byte{1, 2, 3, 4, 5, 6, 0, 0, 2, 0})
	if !ok || ev != (hciEvent{Addr: 0x060504030201, ConnType: 2, Connected: false}) {
		t.Errorf("got %+v, %v", ev, ok)
	}
}
