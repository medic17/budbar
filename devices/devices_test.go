package devices_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"openfit-dolby/bt"
	"openfit-dolby/devices"
	"openfit-dolby/devices/devicestest"
)

func TestIdentify(t *testing.T) {
	for _, tc := range []struct {
		p    bt.PairedDevice
		want string
	}{
		{devicestest.ShokzPaired, "shokz-openfit-2-plus"},
		{devicestest.BoseBudsPaired, "bose-qc-ultra-earbuds-2"},
		{devicestest.BoseHPPaired, "bose-qc-ultra-headphones-2"},
		// Same chip maker's IDs as the Shokz, but not a Shokz.
		{devicestest.JBLTunePaired, ""},
		{devicestest.JBLVibePaired, ""},
	} {
		m, ok := devices.Identify(tc.p)
		if m.ID != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: %q, %t; want %q", tc.p.Name, m.ID, ok, tc.want)
		}
	}
	// A Shokz name alone, or the Shokz service alone, is not enough.
	nameOnly := devicestest.JBLTunePaired
	nameOnly.Name = "OpenFit 2+ by Shokz"
	serviceOnly := devicestest.ShokzPaired
	serviceOnly.Name = "OpenFit Pro"
	for _, p := range []bt.PairedDevice{nameOnly, serviceOnly} {
		if m, ok := devices.Identify(p); ok {
			t.Errorf("%+v identified as %s", p, m.ID)
		}
	}
}

func TestAddr(t *testing.T) {
	if s := devices.FormatAddr(0xA00CE275632F); s != "A0:0C:E2:75:63:2F" {
		t.Errorf("FormatAddr = %q", s)
	}
	for _, s := range []string{"A0:0C:E2:75:63:2F", "a0-0c-e2-75-63-2f", "A00CE275632F"} {
		if a, ok := devices.ParseAddr(s); !ok || a != 0xA00CE275632F {
			t.Errorf("ParseAddr(%q) = %X, %t", s, a, ok)
		}
	}
	for _, s := range []string{"", "A0:0C:E2:75:63", "earbuds", "A0:0C:E2:75:63:2G"} {
		if _, ok := devices.ParseAddr(s); ok {
			t.Errorf("ParseAddr(%q) accepted", s)
		}
	}
}

func TestEmptyStore(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired)
	if list, err := s.Load(); err != nil || len(list) != 0 {
		t.Errorf("Load = %v, %v", list, err)
	}
	c, err := s.Candidates()
	if err != nil || fmt.Sprint(c) != fmt.Sprint([]devices.Device{devicestest.BoseBudsDevice, devicestest.BoseHPDevice, devicestest.ShokzDevice}) {
		t.Errorf("Candidates = %v, %v", c, err)
	}
}

func TestAddRemove(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired)
	if err := s.Add(devicestest.ShokzDevice); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(devicestest.ShokzDevice); err == nil {
		t.Error("added twice")
	}
	jbl := devices.Device{Addr: devicestest.JBLTunePaired.Addr, Model: "shokz-openfit-2-plus", Name: "JBL Tune Flex 2"}
	if err := s.Add(jbl); err == nil {
		t.Error("added an untested device")
	}
	if err := s.Add(devicestest.BoseHPDevice); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Load()
	if fmt.Sprint(list) != fmt.Sprint([]devices.Device{devicestest.ShokzDevice, devicestest.BoseHPDevice}) {
		t.Errorf("list %v", list)
	}
	c, _ := s.Candidates()
	if fmt.Sprint(c) != fmt.Sprint([]devices.Device{devicestest.BoseBudsDevice}) {
		t.Errorf("candidates %v", c)
	}
	if err := s.Remove(devicestest.ShokzDevice.Addr); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(devicestest.ShokzDevice.Addr); !errors.Is(err, devices.ErrNotAdded) {
		t.Errorf("second remove: %v", err)
	}
	list, _ = s.Load()
	if fmt.Sprint(list) != fmt.Sprint([]devices.Device{devicestest.BoseHPDevice}) {
		t.Errorf("list %v", list)
	}
	b, _ := os.ReadFile(s.Path)
	want := `{
  "devices": [
    {
      "address": "68:F2:1F:0D:F0:42",
      "model": "bose-qc-ultra-headphones-2",
      "name": "Bose QC Ultra 2 HP"
    }
  ]
}
`
	if string(b) != want {
		t.Errorf("file:\n%s\nwant:\n%s", b, want)
	}
}

func TestVerify(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired, devicestest.ShokzDevice, devicestest.BoseBudsDevice)
	if d, err := s.Verify(devicestest.ShokzDevice.Addr, devices.Shokz); err != nil || d != devicestest.ShokzDevice {
		t.Errorf("Shokz: %v, %v", d, err)
	}
	if d, err := s.Verify(devicestest.BoseBudsDevice.Addr, devices.Bose); err != nil || d != devicestest.BoseBudsDevice {
		t.Errorf("Bose: %v, %v", d, err)
	}
	for name, tc := range map[string]struct {
		addr uint64
		kind devices.Kind
	}{
		"wrong kind":      {devicestest.ShokzDevice.Addr, devices.Bose},
		"not added":       {devicestest.BoseHPDevice.Addr, devices.Bose},
		"untested":        {devicestest.JBLTunePaired.Addr, devices.Shokz},
		"unknown address": {0x112233445566, devices.Shokz},
	} {
		if _, err := s.Verify(tc.addr, tc.kind); err == nil {
			t.Errorf("%s: allowed", name)
		}
	}
}

// A hand-edited file cannot make an untested device pass, and a device that
// is no longer paired is refused.
func TestVerifyChecksWindowsRecords(t *testing.T) {
	s := devicestest.Store(t, devicestest.AllPaired)
	edited := `{"devices": [{"address": "38:D5:18:9D:7E:9A", "model": "shokz-openfit-2-plus", "name": "OpenFit 2+ by Shokz"}]}`
	if err := os.WriteFile(s.Path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(devicestest.JBLTunePaired.Addr, devices.Shokz); !errors.Is(err, devices.ErrNotIdentified) {
		t.Errorf("edited file: %v", err)
	}

	s = devicestest.Store(t, devicestest.AllPaired, devicestest.ShokzDevice)
	s.Paired = func() ([]bt.PairedDevice, error) { return []bt.PairedDevice{devicestest.BoseBudsPaired}, nil }
	if _, err := s.Verify(devicestest.ShokzDevice.Addr, devices.Shokz); err == nil || !strings.Contains(err.Error(), "not paired") {
		t.Errorf("unpaired: %v", err)
	}

	s.Paired = func() ([]bt.PairedDevice, error) { return nil, errors.New("no radio") }
	if _, err := s.Verify(devicestest.ShokzDevice.Addr, devices.Shokz); err == nil || !strings.Contains(err.Error(), "no radio") {
		t.Errorf("no records: %v", err)
	}
}

func TestLoadRejectsBadFile(t *testing.T) {
	s := devicestest.Store(t, nil)
	for _, content := range []string{"{", `{"devices": [{"address": "nope"}]}`} {
		os.WriteFile(s.Path, []byte(content), 0o644)
		if _, err := s.Load(); err == nil {
			t.Errorf("%q loaded", content)
		}
	}
}

func TestMatch(t *testing.T) {
	list := devicestest.AllTestedDevices
	for _, tc := range []struct {
		query string
		want  []devices.Device
	}{
		{"openfit", []devices.Device{devicestest.ShokzDevice}},
		{"SHOKZ", []devices.Device{devicestest.ShokzDevice}},
		{"bose", []devices.Device{devicestest.BoseBudsDevice, devicestest.BoseHPDevice}},
		{"earbuds", []devices.Device{devicestest.BoseBudsDevice}},
		{"bose earbuds", []devices.Device{devicestest.BoseBudsDevice}},
		// "headphones" is in the model's name, not the device's.
		{"headphones", []devices.Device{devicestest.BoseHPDevice}},
		{"68:F2:1F:0D:F0:42", []devices.Device{devicestest.BoseHPDevice}},
		{"jbl", nil},
		{"", nil},
		{"  ", nil},
	} {
		if got := devices.Match(list, tc.query); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("Match(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestPick(t *testing.T) {
	all := devicestest.AllTestedDevices
	buds, hp := devicestest.BoseBudsDevice, devicestest.BoseHPDevice
	only := func(want uint64) func(uint64) (bool, error) {
		return func(a uint64) (bool, error) { return a == want, nil }
	}
	none := only(0)
	for _, tc := range []struct {
		name      string
		list      []devices.Device
		kind      devices.Kind
		query     string
		connected func(uint64) (bool, error)
		want      devices.Device // zero: an ErrPick
	}{
		{"only Shokz", all, devices.Shokz, "", none, devicestest.ShokzDevice},
		{"query", all, devices.Bose, "headphones", none, hp},
		{"query by address", all, devices.Bose, "68F21F3B5CB7", none, buds},
		{"two, one connected", all, devices.Bose, "", only(hp.Addr), hp},
		{"two, none connected", all, devices.Bose, "", none, devices.Device{}},
		{"two match the query", all, devices.Bose, "bose", only(hp.Addr), devices.Device{}},
		{"query of another kind", all, devices.Bose, "shokz", none, devices.Device{}},
		{"none of kind", []devices.Device{buds}, devices.Shokz, "", none, devices.Device{}},
		{"none added", nil, devices.Bose, "", none, devices.Device{}},
	} {
		got, err := devices.Pick(tc.list, tc.kind, tc.query, tc.connected)
		if got != tc.want || (tc.want == devices.Device{}) != errors.Is(err, devices.ErrPick) {
			t.Errorf("%s: %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestOfKind(t *testing.T) {
	if got := devices.OfKind(devicestest.AllTestedDevices, devices.Bose); fmt.Sprint(got) != fmt.Sprint([]devices.Device{devicestest.BoseBudsDevice, devicestest.BoseHPDevice}) {
		t.Errorf("Bose: %v", got)
	}
}
