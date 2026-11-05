package bt

import (
	"testing"

	"golang.org/x/sys/windows"
)

// Instance IDs as Windows lists them on the PC the tools were tested on.
var testInstanceIDs = []string{
	`BTHENUM\{0000EB06-D102-11E1-9B23-00025B00A5A5}_VID&0001009E_PID&4062\8&2E8BD554&0&68F21F3B5CB7_C00000000`,
	`BTHENUM\{66666666-6666-6666-6666-666666666666}_VID&000102B0_PID&0000\8&2E8BD554&0&A00CE275632F_C00000000`,
	`BTHENUM\{00001101-0000-1000-8000-00805F9B34FB}_VID&000102B0_PID&0000\8&E4D456A&0&A00CE275632F_C00000000`,
	`BTHENUM\DEV_A00CE275632F\8&2E8BD554&0&BLUETOOTHDEVICE_A00CE275632F`,
	`BTHENUM\{0000FEF0-0000-1000-8000-00805F9B34FB}_VID&000102B0_PID&0000\8&2E8BD554&0&A00CE275632F_C00000000`,
	`BTHENUM\DEV_68F21F3B5CB7\8&2E8BD554&0&BLUETOOTHDEVICE_68F21F3B5CB7`,
	`BTHENUM\{00007034-0000-1000-8000-00805F9B34FB}_LOCALMFG&0046\8&2E8BD554&0&34516F0674BC_C00000000`,
	// A service entry without a device entry is dropped.
	`BTHENUM\{0000110B-0000-1000-8000-00805F9B34FB}_VID&0001009E_PID&4082\8&2E8BD554&0&68F21F0DF042_C00000000`,
	// Not BTHENUM, or not one of the two forms.
	`BTHLE\DEV_68F21F3B5CB7\8&133C847A&0&68F21F3B5CB7`,
	`BTHENUM\DEV_68F21F3B5C\x`,
	`BTHENUM\{BAD}\8&2E8BD554&0&68F21F3B5CB7_C00000000`,
	`BTHENUM\{00001101-0000-1000-8000-00805F9B34FB}_VID&0001009E_PID&4062\8&2E8BD554&0&NOTANADDRESS_C00000000`,
	`garbage`,
}

func guid(t *testing.T, s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestParseInstanceIDs(t *testing.T) {
	got := parseInstanceIDs(testInstanceIDs)
	if len(got) != 2 {
		t.Fatalf("got %+v, want the Bose earbuds and the Shokz", got)
	}
	buds, shokz := got[0], got[1]
	if shokz.Addr != 0xA00CE275632F || shokz.VendorID != 0x000102B0 || shokz.ProductID != 0 || len(shokz.Services) != 3 {
		t.Errorf("Shokz %+v", shokz)
	}
	if !shokz.HasService(guid(t, "{0000FEF0-0000-1000-8000-00805F9B34FB}")) {
		t.Error("Shokz lacks its Shokz service")
	}
	if buds.Addr != 0x68F21F3B5CB7 || buds.VendorID != 0x0001009E || buds.ProductID != 0x4062 || len(buds.Services) != 1 {
		t.Errorf("Bose earbuds %+v", buds)
	}
	if buds.HasService(guid(t, "{0000FEF0-0000-1000-8000-00805F9B34FB}")) {
		t.Error("Bose earbuds have the Shokz service")
	}
}
