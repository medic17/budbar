package bt

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	bluetoothAPIs               = windows.NewLazySystemDLL("BluetoothApis.dll")
	procBluetoothFindFirstRadio = bluetoothAPIs.NewProc("BluetoothFindFirstRadio")
	procBluetoothFindRadioClose = bluetoothAPIs.NewProc("BluetoothFindRadioClose")
	procBluetoothGetDeviceInfo  = bluetoothAPIs.NewProc("BluetoothGetDeviceInfo")
)

// deviceInfo is BLUETOOTH_DEVICE_INFO.
type deviceInfo struct {
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

// FirstRadio opens the first local Bluetooth radio. The caller closes it.
func FirstRadio() (windows.Handle, error) {
	params := struct{ Size uint32 }{4} // BLUETOOTH_FIND_RADIO_PARAMS
	var radio windows.Handle
	find, _, err := procBluetoothFindFirstRadio.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&radio)))
	if find == 0 {
		return 0, fmt.Errorf("no Bluetooth radio: %w", err)
	}
	procBluetoothFindRadioClose.Call(find)
	return radio, nil
}

// Connected asks Windows whether the paired device at addr is connected to
// this PC. It only reads Windows' own records; nothing is sent over the air.
func Connected(addr uint64) (bool, error) {
	radio, err := FirstRadio()
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(radio)
	info := deviceInfo{Address: addr}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, _ := procBluetoothGetDeviceInfo.Call(uintptr(radio), uintptr(unsafe.Pointer(&info))); r != 0 {
		return false, fmt.Errorf("BluetoothGetDeviceInfo: %w", syscall.Errno(r))
	}
	return info.Connected != 0, nil
}
