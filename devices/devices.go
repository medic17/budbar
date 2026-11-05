// Package devices keeps the list of earbuds and headphones the tools may talk
// to, and recognises the tested models among the paired devices.
//
// A device is added once (budbar add, or the tray's Add device menu) and
// stays in devices.json. Only paired devices whose Windows records identify
// them as a tested model can be added, and Verify checks both again before
// every connection, so neither a stale list nor a hand-edited file can point
// the tools at an untested device. Nothing here sends anything over the air.
package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"

	"openfit-dolby/bt"
)

// Kind says which protocol a model speaks, and so which client and frame
// allowlist apply.
type Kind int

const (
	Shokz Kind = iota + 1 // Dolby Audio switch
	Bose                  // BMAP listening mode
)

func (k Kind) String() string {
	switch k {
	case Shokz:
		return "Shokz"
	case Bose:
		return "Bose"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Model is one tested product.
type Model struct {
	ID    string // stored in devices.json
	Name  string
	Kind  Kind
	match func(bt.PairedDevice) bool
}

// shokzService is the SDP service class (16-bit UUID 0xFEF0) the Shokz app
// uses; Windows lists it as "SHOKZ SPP".
var shokzService = windows.GUID{
	Data1: 0x0000FEF0, Data2: 0x0000, Data3: 0x1000,
	Data4: [8]byte{0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB},
}

// boseVendorID is Bose (0x009E) as numbered by the Bluetooth SIG (source 1).
const boseVendorID = 0x0001009E

// Models are the tested products. The Bose ones publish their product ID.
// The Shokz publishes only its chip maker's vendor ID with product 0, which
// other earbuds on this PC share, so it is recognised by its name and the
// Shokz service instead.
var Models = []Model{
	{
		ID: "shokz-openfit-2-plus", Name: "Shokz OpenFit 2+", Kind: Shokz,
		match: func(p bt.PairedDevice) bool {
			return strings.Contains(p.Name, "OpenFit 2+") && p.HasService(shokzService)
		},
	},
	{
		// bosectl's "edith".
		ID: "bose-qc-ultra-earbuds-2", Name: "Bose QC Ultra Earbuds (2nd Gen)", Kind: Bose,
		match: func(p bt.PairedDevice) bool { return p.VendorID == boseVendorID && p.ProductID == 0x4062 },
	},
	{
		// bosectl's "wolverine".
		ID: "bose-qc-ultra-headphones-2", Name: "Bose QC Ultra Headphones (2nd Gen)", Kind: Bose,
		match: func(p bt.PairedDevice) bool { return p.VendorID == boseVendorID && p.ProductID == 0x4082 },
	},
}

// Identify returns the tested model p is, if any.
func Identify(p bt.PairedDevice) (Model, bool) {
	for _, m := range Models {
		if m.match(p) {
			return m, true
		}
	}
	return Model{}, false
}

// ModelByID returns the model stored as id.
func ModelByID(id string) (Model, bool) {
	for _, m := range Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Device is one added device.
type Device struct {
	Addr  uint64
	Model string // a Model ID
	Name  string // the device's own name when it was added
}

func (d Device) String() string { return d.Name }

// Kind returns the kind of d's model, or 0 for an unknown model.
func (d Device) Kind() Kind {
	m, _ := ModelByID(d.Model)
	return m.Kind
}

// FormatAddr writes a Bluetooth address the way Windows Settings shows it.
func FormatAddr(a uint64) string {
	s := fmt.Sprintf("%012X", a)
	return s[0:2] + ":" + s[2:4] + ":" + s[4:6] + ":" + s[6:8] + ":" + s[8:10] + ":" + s[10:12]
}

// ParseAddr reads a Bluetooth address with or without : or - separators.
func ParseAddr(s string) (uint64, bool) {
	hex := strings.NewReplacer(":", "", "-", "").Replace(s)
	if len(hex) != 12 {
		return 0, false
	}
	n, err := strconv.ParseUint(hex, 16, 64)
	return n, err == nil
}

// Errors from Verify.
var (
	ErrNotAdded      = errors.New("not an added device")
	ErrNotIdentified = errors.New("Windows' records do not identify it as the listed model")
)

// Store is the device list in a file, plus the source of Windows' paired
// device records; tests replace both.
type Store struct {
	Path   string
	Paired func() ([]bt.PairedDevice, error)
}

// DefaultPath is devices.json in budbar's folder under %LOCALAPPDATA%, next to
// the installed exe and log.
func DefaultPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "budbar", "devices.json")
}

// Default is the store the tools use.
func Default() Store { return Store{Path: DefaultPath(), Paired: bt.Paired} }

// fileDevice is a Device as written in devices.json.
type fileDevice struct {
	Address string `json:"address"`
	Model   string `json:"model"`
	Name    string `json:"name"`
}

type file struct {
	Devices []fileDevice `json:"devices"`
}

// Load returns the added devices. A missing file means none.
func (s Store) Load() ([]Device, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	var list []Device
	for _, fd := range f.Devices {
		a, ok := ParseAddr(fd.Address)
		if !ok {
			return nil, fmt.Errorf("%s: bad address %q", s.Path, fd.Address)
		}
		list = append(list, Device{Addr: a, Model: fd.Model, Name: fd.Name})
	}
	return list, nil
}

// save writes list through a temporary file, so a reader never sees half a
// file.
func (s Store) save(list []Device) error {
	f := file{Devices: []fileDevice{}}
	for _, d := range list {
		f.Devices = append(f.Devices, fileDevice{Address: FormatAddr(d.Addr), Model: d.Model, Name: d.Name})
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Candidates returns the paired devices of a tested model that are not
// added yet.
func (s Store) Candidates() ([]Device, error) {
	list, err := s.Load()
	if err != nil {
		return nil, err
	}
	paired, err := s.Paired()
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, p := range paired {
		m, ok := Identify(p)
		if !ok || slices.ContainsFunc(list, func(d Device) bool { return d.Addr == p.Addr }) {
			continue
		}
		out = append(out, Device{Addr: p.Addr, Model: m.ID, Name: p.Name})
	}
	return out, nil
}

// Add adds d, which must be one of Candidates.
func (s Store) Add(d Device) error {
	cands, err := s.Candidates()
	if err != nil {
		return err
	}
	if !slices.Contains(cands, d) {
		return fmt.Errorf("%s (%s) is not a paired device of a tested model that is not added yet", d.Name, FormatAddr(d.Addr))
	}
	list, err := s.Load()
	if err != nil {
		return err
	}
	return s.save(append(list, d))
}

// Remove removes the device at addr.
func (s Store) Remove(addr uint64) error {
	list, err := s.Load()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(list, func(d Device) bool { return d.Addr == addr })
	if i < 0 {
		return fmt.Errorf("%s: %w", FormatAddr(addr), ErrNotAdded)
	}
	return s.save(slices.Delete(list, i, i+1))
}

// Verify returns the added device at addr if it is of kind and Windows'
// records still identify it as its model. Every connect calls it first.
func (s Store) Verify(addr uint64, kind Kind) (Device, error) {
	list, err := s.Load()
	if err != nil {
		return Device{}, err
	}
	i := slices.IndexFunc(list, func(d Device) bool { return d.Addr == addr })
	if i < 0 {
		return Device{}, fmt.Errorf("refusing to connect to %s: %w", FormatAddr(addr), ErrNotAdded)
	}
	d := list[i]
	m, ok := ModelByID(d.Model)
	if !ok || m.Kind != kind {
		return Device{}, fmt.Errorf("refusing to connect to %s: listed as model %q, not a %v model", FormatAddr(addr), d.Model, kind)
	}
	paired, err := s.Paired()
	if err != nil {
		return Device{}, err
	}
	j := slices.IndexFunc(paired, func(p bt.PairedDevice) bool { return p.Addr == addr })
	if j < 0 {
		return Device{}, fmt.Errorf("refusing to connect to %s: not paired with this PC", FormatAddr(addr))
	}
	if got, ok := Identify(paired[j]); !ok || got.ID != m.ID {
		return Device{}, fmt.Errorf("refusing to connect to %s: %w (%s)", FormatAddr(addr), ErrNotIdentified, m.Name)
	}
	return d, nil
}

// Match returns the devices in list that query names: an address, or words
// that all appear in the device's name or its model's name, in any case.
func Match(list []Device, query string) []Device {
	if a, ok := ParseAddr(query); ok {
		return slices.DeleteFunc(slices.Clone(list), func(d Device) bool { return d.Addr != a })
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	var out []Device
	for _, d := range list {
		m, _ := ModelByID(d.Model)
		hay := strings.ToLower(d.Name + " " + m.Name)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			out = append(out, d)
		}
	}
	return out
}

// ErrPick means Pick could not settle on one device; the message says why.
var ErrPick = errors.New("no single device chosen")

// pickError carries Pick's explanation and matches ErrPick.
type pickError string

func (e pickError) Error() string        { return string(e) }
func (e pickError) Is(target error) bool { return target == ErrPick }

func pickErrorf(format string, args ...any) error { return pickError(fmt.Sprintf(format, args...)) }

// Pick returns the one added device of kind that query names (see Match).
// With an empty query it is the only device of kind, or of several the only
// one connected to this PC now, as connected reports.
func Pick(list []Device, kind Kind, query string, connected func(uint64) (bool, error)) (Device, error) {
	of := OfKind(list, kind)
	if len(of) == 0 {
		return Device{}, pickErrorf("no %v device added (budbar devices lists the ones you can add)", kind)
	}
	if query != "" {
		of = Match(of, query)
		if len(of) == 0 {
			return Device{}, pickErrorf("no added %v device matches %q (budbar devices lists them)", kind, query)
		}
	} else if len(of) > 1 {
		var now []Device
		for _, d := range of {
			if ok, err := connected(d.Addr); err == nil && ok {
				now = append(now, d)
			}
		}
		if len(now) == 1 {
			return now[0], nil
		}
	}
	if len(of) > 1 {
		names := make([]string, len(of))
		for i, d := range of {
			names[i] = fmt.Sprintf("%s (%s)", d.Name, FormatAddr(d.Addr))
		}
		return Device{}, pickErrorf("%d %v devices match, pick one with -device: %s", len(of), kind, strings.Join(names, ", "))
	}
	return of[0], nil
}

// OfKind returns the devices in list of kind.
func OfKind(list []Device, kind Kind) []Device {
	return slices.DeleteFunc(slices.Clone(list), func(d Device) bool { return d.Kind() != kind })
}
