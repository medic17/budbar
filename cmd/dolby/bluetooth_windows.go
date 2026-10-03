package main

// Whether the earbuds are connected to this PC: a query for the current state
// and events for changes.
//
// Windows reports ACL connects and disconnects of remote devices as
// WM_DEVICECHANGE / DBT_CUSTOMEVENT with GUID_BLUETOOTH_HCI_EVENT, but only to
// a window that registered for notifications on the local radio's handle
// ("Bluetooth and WM_DEVICECHANGE Messages" in the Win32 docs). Events only
// report changes, so the tray also asks BluetoothGetDeviceInfo at startup and
// after sleep or hibernate.

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	bluetoothAPIs                   = windows.NewLazySystemDLL("BluetoothApis.dll")
	procBluetoothFindFirstRadio     = bluetoothAPIs.NewProc("BluetoothFindFirstRadio")
	procBluetoothFindRadioClose     = bluetoothAPIs.NewProc("BluetoothFindRadioClose")
	procBluetoothGetDeviceInfo      = bluetoothAPIs.NewProc("BluetoothGetDeviceInfo")
	procRegisterDeviceNotificationW = user32.NewProc("RegisterDeviceNotificationW")
)

// bluetoothDeviceInfo is BLUETOOTH_DEVICE_INFO.
type bluetoothDeviceInfo struct {
	Size          uint32
	Address       uint64
	ClassOfDevice uint32
	Connected     int32
	Remembered    int32
	Authenticated int32
	LastSeen      windows.Systemtime
	LastUsed      windows.Systemtime
	Name          [248]uint16
}

// firstRadio opens the first local Bluetooth radio. The caller closes it.
func firstRadio() (windows.Handle, error) {
	params := struct{ Size uint32 }{4} // BLUETOOTH_FIND_RADIO_PARAMS
	var radio windows.Handle
	find, _, err := procBluetoothFindFirstRadio.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&radio)))
	if find == 0 {
		return 0, fmt.Errorf("no Bluetooth radio: %w", err)
	}
	procBluetoothFindRadioClose.Call(find)
	return radio, nil
}

// earbudsConnected asks Windows whether the earbuds are connected to this PC.
// It only reads Windows' own records; nothing is sent over the air.
func earbudsConnected() (bool, error) {
	radio, err := firstRadio()
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(radio)
	info := bluetoothDeviceInfo{Address: earbudsAddr}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, _ := procBluetoothGetDeviceInfo.Call(uintptr(radio), uintptr(unsafe.Pointer(&info))); r != 0 {
		return false, fmt.Errorf("BluetoothGetDeviceInfo: %w", syscall.Errno(r))
	}
	return info.Connected != 0, nil
}

const (
	wmDeviceChange   = 0x0219
	dbtCustomEvent   = 0x8006
	dbtDevtypHandle  = 6
	hciConnTypeACL   = 1
	hciEventInfoSize = 10 // BTH_ADDR, connectionType, connected
)

// guidBluetoothHCIEvent is GUID_BLUETOOTH_HCI_EVENT from bthdef.h.
var guidBluetoothHCIEvent = windows.GUID{
	Data1: 0xfc240062, Data2: 0x1541, Data3: 0x49be,
	Data4: [8]byte{0xb4, 0x63, 0x84, 0xc4, 0xdc, 0xd7, 0xbf, 0x7f},
}

// devBroadcastHandle is DEV_BROADCAST_HANDLE; Data is the start of the
// event's buffer.
type devBroadcastHandle struct {
	Size       uint32
	DeviceType uint32
	Reserved   uint32
	Handle     windows.Handle
	DevNotify  uintptr
	EventGUID  windows.GUID
	NameOffset int32
	Data       [1]byte
}

// hciEvent is BTH_HCI_EVENT_INFO.
type hciEvent struct {
	Addr      uint64
	ConnType  byte
	Connected bool
}

// watchBluetooth opens the first Bluetooth radio and registers hwnd for its
// events. The radio handle must stay open for as long as the registration.
func watchBluetooth(hwnd windows.HWND) (windows.Handle, error) {
	radio, err := firstRadio()
	if err != nil {
		return 0, err
	}
	filter := devBroadcastHandle{DeviceType: dbtDevtypHandle, Handle: radio}
	filter.Size = uint32(unsafe.Sizeof(filter))
	const deviceNotifyWindowHandle = 0
	if r, _, err := procRegisterDeviceNotificationW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&filter)), deviceNotifyWindowHandle); r == 0 {
		windows.CloseHandle(radio)
		return 0, fmt.Errorf("RegisterDeviceNotification: %w", err)
	}
	return radio, nil
}

// hciEventFrom reads the DEV_BROADCAST_HANDLE that a DBT_CUSTOMEVENT's
// lParam points to. ok is false for any event other than
// GUID_BLUETOOTH_HCI_EVENT.
func hciEventFrom(h *devBroadcastHandle) (ev hciEvent, ok bool) {
	if h.DeviceType != dbtDevtypHandle || h.EventGUID != guidBluetoothHCIEvent {
		return ev, false
	}
	if uintptr(h.Size) < unsafe.Offsetof(h.Data)+hciEventInfoSize {
		return ev, false
	}
	return parseHCIEvent(unsafe.Slice(&h.Data[0], hciEventInfoSize))
}

// parseHCIEvent decodes BTH_HCI_EVENT_INFO: a little-endian 64-bit address,
// then the connection type and a connected flag.
func parseHCIEvent(b []byte) (hciEvent, bool) {
	if len(b) < hciEventInfoSize {
		return hciEvent{}, false
	}
	return hciEvent{
		Addr:      binary.LittleEndian.Uint64(b),
		ConnType:  b[8],
		Connected: b[9] != 0,
	}, true
}
