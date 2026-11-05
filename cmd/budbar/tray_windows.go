package main

// The tray icon, its menu and hover text, the Ctrl+Q hotkey, and the window
// that receives all of their events.
//
// One hidden top-level window does all of it, on the main thread. Top-level
// rather than message-only (HWND_MESSAGE) because the shell's TaskbarCreated
// is broadcast only to top-level windows. Actions run on the agent's
// goroutines, which hand their results back through SetStatus.
//
// Written directly against Shell_NotifyIcon rather than a systray library,
// which would own the message loop that the hotkey and Bluetooth events need.
// No balloons: status lives in the hover text and the menu header.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"openfit-dolby/bose"
	"openfit-dolby/bt"
	"openfit-dolby/devices"
)

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	shell32                   = windows.NewLazySystemDLL("shell32.dll")
	gdi32                     = windows.NewLazySystemDLL("gdi32.dll")
	procShellNotifyIconW      = shell32.NewProc("Shell_NotifyIconW")
	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenuW           = user32.NewProc("AppendMenuW")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")
	procRegisterHotKey        = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey      = user32.NewProc("UnregisterHotKey")
	procCreateIconIndirect    = user32.NewProc("CreateIconIndirect")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
	procSetDpiAwarenessCtx    = user32.NewProc("SetProcessDpiAwarenessContext")
	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap          = gdi32.NewProc("CreateBitmap")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
)

const (
	wmDestroy     = 0x0002
	wmContextMenu = 0x007B
	wmNull        = 0x0000
	wmHotkey      = 0x0312
	wmApp         = 0x8000
	wmTray        = wmApp + 1 // tray icon callback
	wmRefresh     = wmApp + 2 // an agent published a new status
	wmResumeCheck = wmApp + 3 // time to check the connections after resume

	ninSelect    = 0x0400
	ninKeySelect = 0x0401

	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4
	nifMessage    = 0x01
	nifIcon       = 0x02
	nifTip        = 0x04
	nifShowTip    = 0x80
	notifyIconV4  = 4

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfPopup     = 0x0010
	mfSeparator = 0x0800
	tpmFlags    = 0x0100 | 0x0002 | 0x0080 | 0x0020 // RETURNCMD|RIGHTBUTTON|NONOTIFY|BOTTOMALIGN

	smCxSmIcon = 49

	hotkeyID    = 1
	modControl  = 0x0002
	modNoRepeat = 0x4000
	vkQ         = 'Q'

	cmdTurnOn       = 1
	cmdTurnOff      = 2
	cmdRefresh      = 3
	cmdOpenLog      = 4
	cmdExit         = 5
	cmdRefreshModes = 6
	cmdAddDevice    = 100 // + index in the Add device submenu

	wmSettingChange = 0x001A // also sent when the light/dark theme changes
)

const (
	wmPowerBroadcast      = 0x0218
	pbtAPMSuspend         = 0x0004 // going to sleep or hibernate
	pbtAPMResumeAutomatic = 0x0012 // back from sleep or hibernate, every time
)

// connectSettle is how long after the earbuds connect the tray waits before
// reading, so their services are up before it asks. resumeSettle gives
// Bluetooth the same time after sleep or hibernate before checking.
const (
	connectSettle = 5 * time.Second
	resumeSettle  = 5 * time.Second
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type winMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
	Private uint32
}

type notifyIconData struct {
	Size             uint32
	Wnd              windows.HWND
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         windows.GUID
	BalloonIcon      windows.Handle
}

// iconKey picks one of the six icons: a colour per state, filled or outline.
type iconKey struct {
	state dolbyState
	busy  bool
}

// boseIconKey picks one of the four Bose icons: the look for a dark or light
// taskbar, normal or dimmed after a failed check.
type boseIconKey struct {
	dark, dim bool
}

// The tray shows one device at a time: of the connected ones, the one that
// connected last; while none is, the first Shokz earbuds' Dolby state.
// It keeps one agent per device in devices.json and rereads the file at
// startup, when its menu opens and on every Bluetooth event, so devices
// added or removed with the CLI take effect without a restart.
type tray struct {
	hwnd           windows.HWND
	dial           dialFunc // for the Shokz agents
	shokz          []*agent
	bose           []*boseAgent
	retired        []func() // waits for agents of removed devices
	log            *appLog
	taskbarCreated uint32
	icons          map[iconKey]windows.Handle
	boseIcons      map[boseIconKey]windows.Handle
	dark           bool // taskbar theme; read and used on the window's thread
	hotkeyErr      error
	radio          windows.Handle

	mu     sync.Mutex
	recent lastConnected
}

// theTray is the single instance the window procedure dispatches to; Windows
// callbacks carry no Go context.
var theTray *tray

// runTray runs the tray until Exit is chosen and returns the exit code.
func runTray(dial dialFunc, stderr io.Writer) int {
	// The window and its message loop must stay on one OS thread.
	runtime.LockOSThread()

	if !claimSingleInstance() {
		fmt.Fprintln(stderr, "budbar: the tray is already running")
		return 1
	}
	log := openAppLog(os.Stderr)
	defer log.Close()

	t := &tray{log: log, dial: dial}
	theTray = t
	if err := t.create(); err != nil {
		t.logf("tray setup failed: %v", err)
		fmt.Fprintln(stderr, "budbar: tray setup failed:", err)
		return 1
	}
	t.logf("tray started (pid %d, devices from %s, dark taskbar %t)", os.Getpid(), store.Path, t.dark)
	if t.hotkeyErr != nil {
		t.logf("Ctrl+Q not available: %v", t.hotkeyErr)
	}
	t.syncDevices("startup")
	t.refresh()
	t.loop()
	for _, b := range t.bose {
		b.Stop()
	}
	for _, a := range t.shokz {
		a.Wait()
	}
	for _, b := range t.bose {
		b.Wait()
	}
	for _, wait := range t.retired {
		wait()
	}
	t.logf("tray exited")
	return 0
}

func (t *tray) logf(format string, args ...any) {
	fmt.Fprintf(t.log, format+"\n", args...)
}

// claimSingleInstance takes the named mutex that marks the running tray.
// Windows drops it however the process exits, so there is no stale lock.
func claimSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(`Local\budbar-tray`)
	if err != nil {
		return true
	}
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, syscall.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return false
	}
	return true
}

// published is called from the agents' goroutines with each new status. It
// only records whether the device is connected and pokes the window; all
// Shell_NotifyIcon calls happen on the thread that owns the icon.
func (t *tray) published(addr uint64, connected bool) {
	t.mu.Lock()
	t.recent.set(addr, connected)
	t.mu.Unlock()
	procPostMessageW.Call(uintptr(t.hwnd), wmRefresh, 0, 0)
}

// syncDevices starts an agent for each device in devices.json that has none
// and stops those of devices no longer in it. It runs on the window's
// thread.
func (t *tray) syncDevices(why string) {
	list, err := store.Load()
	if err != nil {
		t.logf("reading the device list (%s): %v", why, err)
		return
	}
	listed := map[uint64]bool{}
	for _, d := range list {
		listed[d.Addr] = true
		if t.agentFor(d.Addr) {
			continue
		}
		switch d.Kind() {
		case devices.Shokz:
			a := newAgent(d, t.dial, func() (bool, error) { return bt.Connected(d.Addr) }, t.log,
				func(s Status) { t.published(d.Addr, s.Connected) })
			t.shokz = append(t.shokz, a)
			t.logf("watching %v %012X (%s)", d, d.Addr, why)
			a.Check(why)
		case devices.Bose:
			b := newBoseAgent(d, bose.Dial, func() (bool, error) { return bose.Connected(d.Addr) }, t.log,
				func(s BoseStatus) { t.published(d.Addr, s.Connected) })
			t.bose = append(t.bose, b)
			t.logf("watching %v %012X (%s)", d, d.Addr, why)
			b.Check(why)
		default:
			t.logf("ignoring %v %012X: unknown model %q", d, d.Addr, d.Model)
		}
	}
	t.shokz = slices.DeleteFunc(t.shokz, func(a *agent) bool {
		if listed[a.dev.Addr] {
			return false
		}
		t.logf("%v %012X removed from the device list", a.dev, a.dev.Addr)
		a.Disconnected("removed")
		t.retired = append(t.retired, a.Wait)
		t.published(a.dev.Addr, false)
		return true
	})
	t.bose = slices.DeleteFunc(t.bose, func(b *boseAgent) bool {
		if listed[b.dev.Addr] {
			return false
		}
		t.logf("%v %012X removed from the device list", b.dev, b.dev.Addr)
		b.Stop()
		t.retired = append(t.retired, b.Wait)
		t.published(b.dev.Addr, false)
		return true
	})
}

// agentFor reports whether a device at addr has an agent.
func (t *tray) agentFor(addr uint64) bool {
	return slices.ContainsFunc(t.shokz, func(a *agent) bool { return a.dev.Addr == addr }) ||
		slices.ContainsFunc(t.bose, func(b *boseAgent) bool { return b.dev.Addr == addr })
}

// create makes the window, icons, hotkey and Bluetooth registration. It must
// run on the thread that runs the message loop.
func (t *tray) create() error {
	// Per-monitor DPI awareness so SM_CXSMICON reports the real tray icon size
	// and the icon is drawn at it instead of being upscaled from 16px.
	const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	procSetDpiAwarenessCtx.Call(perMonitorAwareV2)

	size, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	if size == 0 {
		size = 16
	}
	colors := map[dolbyState]uint32{stateOff: colorOff, stateOn: colorOn, stateUnknown: colorUnknown}
	t.icons = map[iconKey]windows.Handle{}
	for state, rgb := range colors {
		for _, busy := range []bool{false, true} {
			t.icons[iconKey{state, busy}] = makeIcon(int(size), headphonePixels(int(size), rgb, busy))
		}
	}
	t.boseIcons = map[boseIconKey]windows.Handle{}
	for _, dark := range []bool{false, true} {
		for _, dim := range []bool{false, true} {
			px, err := bosePixels(int(size), dark, dim)
			if err != nil {
				return fmt.Errorf("Bose icon: %w", err)
			}
			t.boseIcons[boseIconKey{dark, dim}] = makeIcon(int(size), px)
		}
	}
	t.dark = taskbarDark()

	inst, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	cls, _ := windows.UTF16PtrFromString("budbar-tray")
	wc := wndClassEx{
		WndProc:   windows.NewCallback(wndProc),
		Instance:  windows.Handle(inst),
		ClassName: cls,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	title, _ := windows.UTF16PtrFromString("budbar")
	h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, inst, 0)
	if h == 0 {
		return err
	}
	t.hwnd = windows.HWND(h)

	tc, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(tc)))
	t.taskbarCreated = uint32(r)

	if r, _, err := procRegisterHotKey.Call(h, hotkeyID, modControl|modNoRepeat, vkQ); r == 0 {
		t.hotkeyErr = err
	}
	if radio, err := watchBluetooth(t.hwnd); err != nil {
		t.logf("re-reading on connect is off: %v", err)
	} else {
		t.radio = radio
	}

	t.addIcon()
	return nil
}

// shown returns the agent of the device the tray shows: the connected one
// that connected last, else the first Shokz earbuds. At most one is non-nil;
// both are nil when no device is connected and no Shokz earbuds are added.
func (t *tray) shown() (*agent, *boseAgent) {
	t.mu.Lock()
	addr, ok := t.recent.latest()
	t.mu.Unlock()
	if ok {
		for _, a := range t.shokz {
			if a.dev.Addr == addr {
				return a, nil
			}
		}
		for _, b := range t.bose {
			if b.dev.Addr == addr {
				return nil, b
			}
		}
	}
	if len(t.shokz) > 0 {
		return t.shokz[0], nil
	}
	return nil, nil
}

// shokzTarget is the earbuds that Turn on, Turn off and Ctrl+Q act on: the
// shown ones, else the first connected Shokz earbuds, else nil.
func (t *tray) shokzTarget() *agent {
	if a, _ := t.shown(); a != nil && a.Status().Connected {
		return a
	}
	for _, a := range t.shokz {
		if a.Status().Connected {
			return a
		}
	}
	return nil
}

// look returns the icon and the two text lines for the device the tray
// shows.
func (t *tray) look() (icon windows.Handle, head, detail string) {
	a, b := t.shown()
	switch {
	case b != nil:
		s := b.Status()
		return t.boseIcons[boseIconKey{t.dark, s.Failed}], boseHeadline(b.dev, s), s.Detail
	case a != nil:
		s := a.Status()
		return t.icons[iconKey{s.State, s.Busy}], headline(s), detailLine(s)
	}
	grey := t.icons[iconKey{stateUnknown, false}]
	if len(t.bose) == 0 {
		return grey, "No devices added", "right-click to add one"
	}
	var names []string
	for _, b := range t.bose {
		names = append(names, b.dev.Name)
	}
	return grey, "Not connected", strings.Join(names, ", ")
}
func (t *tray) nid() notifyIconData {
	var n notifyIconData
	n.Size = uint32(unsafe.Sizeof(n))
	n.Wnd = t.hwnd
	n.ID = 1
	return n
}

func (t *tray) addIcon() {
	icon, head, detail := t.look()
	n := t.nid()
	n.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	n.CallbackMessage = wmTray
	n.Icon = icon
	copyUTF16(n.Tip[:], head+"\n"+detail)
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&n)))
	n.TimeoutOrVersion = notifyIconV4
	procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&n)))
}

func (t *tray) removeIcon() {
	n := t.nid()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&n)))
}

func (t *tray) refresh() {
	icon, head, detail := t.look()
	n := t.nid()
	n.Flags = nifIcon | nifTip | nifShowTip
	n.Icon = icon
	copyUTF16(n.Tip[:], head+"\n"+detail)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

// headline is the first line of the hover text and the menu header.
func headline(s Status) string {
	return "Dolby Audio: " + s.State.String()
}

// detailLine is the second line: progress while busy, otherwise when the
// state was read or why that failed.
func detailLine(s Status) string {
	if s.Busy {
		return "working..."
	}
	if s.Detail == "" {
		return "starting..."
	}
	return s.Detail
}

// boseHeadline is a Bose device's first line: its name and, once read, its
// mode.
func boseHeadline(dev devices.Device, b BoseStatus) string {
	if b.Mode == "" {
		return dev.String() + ": reading..."
	}
	return dev.String() + ": " + b.Mode
}

func (t *tray) showMenu(x, y int32) {
	t.syncDevices("menu")
	target := t.shokzTarget()
	var s Status
	if target != nil {
		s = target.Status()
	}
	_, ba := t.shown()
	_, head, detail := t.look()
	cands, candErr := store.Candidates()
	if candErr != nil {
		t.logf("listing devices to add: %v", candErr)
	}

	m, _, _ := procCreatePopupMenu.Call()
	defer procDestroyMenu.Call(m) // also destroys the submenu
	add := func(menu uintptr, flags uint32, id uintptr, text string) {
		p, _ := windows.UTF16PtrFromString(text)
		procAppendMenuW.Call(menu, uintptr(flags), id, uintptr(unsafe.Pointer(p)))
	}
	enabled := func(ok bool) uint32 {
		if ok {
			return mfString
		}
		return mfString | mfGrayed
	}
	add(m, mfString|mfGrayed, 0, head)
	add(m, mfString|mfGrayed, 0, detail)
	if t.hotkeyErr != nil {
		add(m, mfString|mfGrayed, 0, "Ctrl+Q not available (see log)")
	}
	add(m, mfSeparator, 0, "")
	if len(t.shokz) > 0 {
		ready := target != nil && !s.Busy
		add(m, enabled(ready && s.State != stateOn), cmdTurnOn, "Turn on")
		add(m, enabled(ready && s.State != stateOff), cmdTurnOff, "Turn off")
	}
	add(m, enabled(ba != nil || (target != nil && !s.Busy)), cmdRefresh, "Refresh")
	if len(t.bose) > 0 {
		add(m, enabled(ba != nil), cmdRefreshModes, "Refresh modes")
	}
	sub, _, _ := procCreatePopupMenu.Call()
	switch {
	case candErr != nil:
		add(sub, mfString|mfGrayed, 0, "Can't list paired devices (see log)")
	case len(cands) == 0:
		add(sub, mfString|mfGrayed, 0, "No other paired device of a tested model")
	}
	for i, d := range cands {
		mod, _ := devices.ModelByID(d.Model)
		add(sub, mfString, cmdAddDevice+uintptr(i), d.Name+"  ("+mod.Name+")")
	}
	add(m, mfPopup, sub, "Add device")
	add(m, mfString, cmdOpenLog, "Open log")
	add(m, mfSeparator, 0, "")
	add(m, mfString, cmdExit, "Exit")

	// Without this the menu doesn't close when you click elsewhere.
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(m, tpmFlags, uintptr(x), uintptr(y), 0, uintptr(t.hwnd), 0)
	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)

	switch {
	case cmd == cmdTurnOn && target != nil:
		target.Do(actOn, "menu")
	case cmd == cmdTurnOff && target != nil:
		target.Do(actOff, "menu")
	case cmd == cmdRefresh && ba != nil:
		ba.Refresh("menu", false)
	case cmd == cmdRefresh && target != nil:
		target.Do(actRefresh, "menu")
	case cmd == cmdRefreshModes && ba != nil:
		ba.Refresh("menu: refresh modes", true)
	case cmd >= cmdAddDevice && cmd < cmdAddDevice+uintptr(len(cands)):
		d := cands[cmd-cmdAddDevice]
		if err := store.Add(d); err != nil {
			t.logf("menu: adding %v %012X failed: %v", d, d.Addr, err)
		} else {
			t.logf("menu: added %v %012X (%s)", d, d.Addr, d.Model)
		}
		t.syncDevices("added")
		t.refresh()
	case cmd == cmdOpenLog:
		openLog()
	case cmd == cmdExit:
		t.logf("menu: exit")
		procDestroyWindow.Call(uintptr(t.hwnd))
	}
}
func openLog() {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(logPath())
	windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

// onDeviceChange handles the radio's Bluetooth events. Every HCI event is
// logged, with what BluetoothGetDeviceInfo reports at that moment for an
// added device. Each added device's ACL connect schedules a read; its
// disconnect shows it as not connected.
func (t *tray) onDeviceChange(wparam, lparam uintptr) {
	if wparam != dbtCustomEvent || lparam == 0 {
		return
	}
	// lparam is a pointer Windows passed as an integer; reading it through
	// &lparam converts it without uintptr arithmetic.
	ev, ok := hciEventFrom(*(**devBroadcastHandle)(unsafe.Pointer(&lparam)))
	if !ok {
		return
	}
	t.syncDevices("bluetooth event")
	if !t.agentFor(ev.Addr) {
		t.logf("bluetooth: %012X type %d connected=%t (not an added device)", ev.Addr, ev.ConnType, ev.Connected)
		return
	}
	now, err := bt.Connected(ev.Addr)
	t.logf("bluetooth: %012X type %d connected=%t (connected per Windows: %t, err=%v)",
		ev.Addr, ev.ConnType, ev.Connected, now, err)
	if ev.ConnType != hciConnTypeACL {
		return
	}
	for _, a := range t.shokz {
		switch {
		case ev.Addr == a.dev.Addr && ev.Connected:
			a.Connected(a.dev.Name+" connected", connectSettle)
		case ev.Addr == a.dev.Addr:
			a.Disconnected("disconnected")
		}
	}
	for _, b := range t.bose {
		switch {
		case ev.Addr == b.dev.Addr && ev.Connected:
			b.Connected(b.dev.Name+" connected", connectSettle)
		case ev.Addr == b.dev.Addr:
			b.Disconnected("disconnected")
		}
	}
}

// onPower greys the icon before sleep or hibernate and checks the
// connections again after resume, since events during sleep are lost.
func (t *tray) onPower(wparam uintptr) {
	switch wparam {
	case pbtAPMSuspend:
		for _, a := range t.shokz {
			a.Disconnected("sleep")
		}
		for _, b := range t.bose {
			b.Disconnected("sleep")
		}
	case pbtAPMResumeAutomatic:
		t.logf("resumed; checking the connections in %s", resumeSettle)
		// Check on the window's thread, which owns the agent lists.
		time.AfterFunc(resumeSettle, func() {
			procPostMessageW.Call(uintptr(t.hwnd), wmResumeCheck, 0, 0)
		})
	}
}

// resumeCheck asks Windows about every device's connection after resume.
func (t *tray) resumeCheck() {
	for _, a := range t.shokz {
		a.Check("resume")
	}
	for _, b := range t.bose {
		b.Check("resume")
	}
}

// onSettingChange picks the Bose icon's look again when the taskbar theme
// changes. Windows sends WM_SETTINGCHANGE for many settings; reading one
// registry value each time is cheap.
func (t *tray) onSettingChange() {
	if dark := taskbarDark(); dark != t.dark {
		t.dark = dark
		t.logf("taskbar theme changed: dark=%t", dark)
		t.refresh()
	}
}

// loop runs the message loop until the window is destroyed.
func (t *tray) loop() {
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwnd windows.HWND, msg uint32, wparam, lparam uintptr) uintptr {
	t := theTray
	switch {
	case msg == wmTray:
		switch uint32(lparam & 0xFFFF) {
		case wmContextMenu, ninSelect, ninKeySelect:
			x := int32(int16(wparam & 0xFFFF))
			y := int32(int16((wparam >> 16) & 0xFFFF))
			t.showMenu(x, y)
		}
		return 0
	case msg == wmRefresh:
		t.refresh()
		return 0
	case msg == wmHotkey && wparam == hotkeyID:
		if a := t.shokzTarget(); a != nil {
			a.Do(actToggle, "Ctrl+Q")
		} else {
			t.logf("toggle (Ctrl+Q) ignored: no Shokz earbuds connected")
		}
		return 0
	case msg == wmResumeCheck:
		t.resumeCheck()
		return 0
	case msg == wmDeviceChange:
		t.onDeviceChange(wparam, lparam)
		return 1
	case msg == wmPowerBroadcast:
		t.onPower(wparam)
		return 1
	case msg == wmSettingChange:
		t.onSettingChange()
		return 0
	case msg == t.taskbarCreated && msg != 0:
		t.addIcon() // Explorer restarted and forgot every tray icon
		return 0
	case msg == wmDestroy:
		procUnregisterHotKey.Call(uintptr(hwnd), hotkeyID)
		t.removeIcon()
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return r
}

func copyUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

// --- icons --------------------------------------------------------------------

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	Mask     windows.Handle
	Color    windows.Handle
}

// makeIcon turns size×size pixels of 0xAARRGGBB (top row first, straight
// alpha) into an icon handle: a 32-bit top-down bitmap whose alpha channel
// carries the shape, plus the empty mask that CreateIconIndirect requires.
func makeIcon(size int, pixels []uint32) windows.Handle {
	bi := bitmapInfoHeader{Width: int32(size), Height: -int32(size), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	color, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 {
		return 0
	}
	copy(unsafe.Slice((*uint32)(bits), size*size), pixels)
	maskBits := make([]byte, ((size+15)/16*2)*size)
	mask, _, _ := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	ii := iconInfo{FIcon: 1, Mask: windows.Handle(mask), Color: windows.Handle(color)}
	h, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	procDeleteObject.Call(color)
	procDeleteObject.Call(mask)
	return windows.Handle(h)
}
