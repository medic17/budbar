// Package devicestest provides paired-device records and device lists for
// tests. The records are copied from Windows on the PC the tools were tested
// on.
package devicestest

import (
	"testing"

	"golang.org/x/sys/windows"

	"openfit-dolby/bt"
	"openfit-dolby/devices"
)

func guid(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

var (
	shokzSPP   = guid("{0000FEF0-0000-1000-8000-00805F9B34FB}")
	serialPort = guid("{00001101-0000-1000-8000-00805F9B34FB}")
	handsFree  = guid("{0000111E-0000-1000-8000-00805F9B34FB}")
)

// Paired records of the tested devices and of two untested earbuds.
var (
	ShokzPaired      = bt.PairedDevice{Addr: 0xA00CE275632F, Name: "OpenFit 2+ by Shokz", VendorID: 0x000102B0, Services: []windows.GUID{handsFree, shokzSPP, serialPort}}
	BoseBudsPaired   = bt.PairedDevice{Addr: 0x68F21F3B5CB7, Name: "Bose QC Ultra 2 Earbuds", VendorID: 0x0001009E, ProductID: 0x4062, Services: []windows.GUID{handsFree, serialPort}}
	BoseHPPaired     = bt.PairedDevice{Addr: 0x68F21F0DF042, Name: "Bose QC Ultra 2 HP", VendorID: 0x0001009E, ProductID: 0x4082, Services: []windows.GUID{handsFree, serialPort}}
	JBLTunePaired    = bt.PairedDevice{Addr: 0x38D5189D7E9A, Name: "JBL Tune Flex 2", VendorID: 0x000102B0, Services: []windows.GUID{handsFree, serialPort}}
	JBLVibePaired    = bt.PairedDevice{Addr: 0x34516F0674BC, Name: "JBL Vibe Flex 2", Services: []windows.GUID{handsFree}}
	AllPaired        = []bt.PairedDevice{BoseBudsPaired, BoseHPPaired, JBLVibePaired, ShokzPaired, JBLTunePaired}
	ShokzDevice      = devices.Device{Addr: ShokzPaired.Addr, Model: "shokz-openfit-2-plus", Name: ShokzPaired.Name}
	BoseBudsDevice   = devices.Device{Addr: BoseBudsPaired.Addr, Model: "bose-qc-ultra-earbuds-2", Name: BoseBudsPaired.Name}
	BoseHPDevice     = devices.Device{Addr: BoseHPPaired.Addr, Model: "bose-qc-ultra-headphones-2", Name: BoseHPPaired.Name}
	AllTestedDevices = []devices.Device{ShokzDevice, BoseBudsDevice, BoseHPDevice}
)

// Store returns a store in a temporary folder whose paired records are
// paired, with added already added.
func Store(t testing.TB, paired []bt.PairedDevice, added ...devices.Device) devices.Store {
	t.Helper()
	s := devices.Store{
		Path:   t.TempDir() + `\devices.json`,
		Paired: func() ([]bt.PairedDevice, error) { return paired, nil },
	}
	for _, d := range added {
		if err := s.Add(d); err != nil {
			t.Fatalf("adding %v: %v", d, err)
		}
	}
	return s
}
