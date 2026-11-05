package bt

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PairedDevice is what Windows' own records say about one paired Bluetooth
// device. Reading them sends nothing over the air.
type PairedDevice struct {
	Addr uint64
	Name string // the name the device gave itself when it paired
	// VendorID and ProductID come from the device's Device ID record, as
	// Windows writes them in its service entries ("VID&0001009E_PID&4062").
	// The upper half of VendorID says who assigned the vendor number (1 is
	// the Bluetooth SIG). Both are 0 when Windows has no such record.
	VendorID, ProductID uint32
	Services            []windows.GUID // service classes Windows made an entry for
}

// HasService reports whether Windows lists service s for the device.
func (p PairedDevice) HasService(s windows.GUID) bool {
	return slices.Contains(p.Services, s)
}

// Paired lists the paired Bluetooth devices on this PC, connected or not.
// It reads the BTHENUM device records, where Windows keeps one entry for
// each device and one for each of its services, then keeps the addresses
// that BluetoothGetDeviceInfo reports as remembered (paired).
func Paired() ([]PairedDevice, error) {
	ids, err := bthenumInstanceIDs()
	if err != nil {
		return nil, err
	}
	found := parseInstanceIDs(ids)
	radio, err := FirstRadio()
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(radio)
	var paired []PairedDevice
	for _, p := range found {
		info, err := getDeviceInfo(radio, p.Addr)
		if err != nil || info.Remembered == 0 {
			continue
		}
		p.Name = windows.UTF16ToString(info.Name[:])
		paired = append(paired, p)
	}
	return paired, nil
}

// bthenumInstanceIDs returns the instance ID of every BTHENUM device record,
// including those of devices that are not connected now.
func bthenumInstanceIDs() ([]string, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "BTHENUM", 0, windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil, fmt.Errorf("listing Bluetooth device records: %w", err)
	}
	defer set.Close()
	var ids []string
	for i := 0; ; i++ {
		data, err := set.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return ids, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing Bluetooth device records: %w", err)
		}
		id, err := set.DeviceInstanceID(data)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
}

// parseInstanceIDs groups BTHENUM instance IDs by device address. Only
// addresses with a device entry are returned, in the order first seen.
func parseInstanceIDs(ids []string) []PairedDevice {
	byAddr := map[uint64]*PairedDevice{}
	var order []uint64
	hasDevice := map[uint64]bool{}
	for _, id := range ids {
		r, ok := parseInstanceID(id)
		if !ok {
			continue
		}
		p := byAddr[r.addr]
		if p == nil {
			p = &PairedDevice{Addr: r.addr}
			byAddr[r.addr] = p
			order = append(order, r.addr)
		}
		if r.device {
			hasDevice[r.addr] = true
			continue
		}
		if !p.HasService(r.service) {
			p.Services = append(p.Services, r.service)
		}
		if r.hasIDs && p.VendorID == 0 && p.ProductID == 0 {
			p.VendorID, p.ProductID = r.vendorID, r.productID
		}
	}
	var out []PairedDevice
	for _, a := range order {
		if hasDevice[a] {
			out = append(out, *byAddr[a])
		}
	}
	return out
}

// instanceRecord is one parsed BTHENUM instance ID.
type instanceRecord struct {
	addr                uint64
	device              bool // the device's own entry rather than a service's
	service             windows.GUID
	hasIDs              bool
	vendorID, productID uint32
}

// parseInstanceID reads the two BTHENUM instance ID forms:
//
//	BTHENUM\DEV_A00CE275632F\8&2E8BD554&0&BLUETOOTHDEVICE_A00CE275632F
//	BTHENUM\{0000FEF0-0000-1000-8000-00805F9B34FB}_VID&000102B0_PID&0000\8&2E8BD554&0&A00CE275632F_C00000000
//
// The first is the device; the second is one of its services, and carries
// the vendor and product IDs when the device published them.
func parseInstanceID(id string) (r instanceRecord, ok bool) {
	parts := strings.Split(strings.ToUpper(id), `\`)
	if len(parts) != 3 || parts[0] != "BTHENUM" {
		return r, false
	}
	if hex, found := strings.CutPrefix(parts[1], "DEV_"); found {
		r.addr, ok = parseHexAddr(hex)
		r.device = true
		return r, ok
	}
	if len(parts[1]) < 38 || parts[1][0] != '{' {
		return r, false
	}
	g, err := windows.GUIDFromString(parts[1][:38])
	if err != nil {
		return r, false
	}
	r.service = g
	for _, f := range strings.Split(parts[1][38:], "_") {
		if v, found := strings.CutPrefix(f, "VID&"); found {
			n, err := strconv.ParseUint(v, 16, 32)
			if err != nil {
				return r, false
			}
			r.vendorID, r.hasIDs = uint32(n), true
		} else if v, found := strings.CutPrefix(f, "PID&"); found {
			n, err := strconv.ParseUint(v, 16, 32)
			if err != nil {
				return r, false
			}
			r.productID = uint32(n)
		}
	}
	// The last part ends "&<address>_C00000000".
	last := parts[2][strings.LastIndex(parts[2], "&")+1:]
	hex, _, _ := strings.Cut(last, "_")
	r.addr, ok = parseHexAddr(hex)
	return r, ok
}

func parseHexAddr(s string) (uint64, bool) {
	if len(s) != 12 {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 16, 64)
	return n, err == nil
}

// getDeviceInfo is BluetoothGetDeviceInfo for addr on radio.
func getDeviceInfo(radio windows.Handle, addr uint64) (deviceInfo, error) {
	info := deviceInfo{Address: addr}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, _ := procBluetoothGetDeviceInfo.Call(uintptr(radio), uintptr(unsafe.Pointer(&info))); r != 0 {
		return info, fmt.Errorf("BluetoothGetDeviceInfo: %w", syscall.Errno(r))
	}
	return info, nil
}
