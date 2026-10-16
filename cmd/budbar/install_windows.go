package main

// Installation: copy the exe somewhere stable and register the scheduled task
// that starts the tray at logon.
//
// The exe is a console program whose manifest asks Windows not to give it a
// console unless it inherits one (consoleAllocationPolicy "detached"), so the
// task starts the tray without a console window.
//
// The task only starts the tray. There is no restart trigger: the tray menu
// has an Exit item, and a restart net would undo it. ExecutionTimeLimit is
// PT0S, meaning no limit, because the tray runs until logoff.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const taskName = "budbar tray"

func installedExePath() string { return filepath.Join(appDir(), "budbar.exe") }

func currentUser() string {
	dom := os.Getenv("USERDOMAIN")
	usr := os.Getenv("USERNAME")
	if dom == "" {
		return usr
	}
	return dom + `\` + usr
}

// copySelf copies the running exe to dst. A running exe cannot be
// overwritten but can be renamed, so an existing copy is moved aside first.
func copySelf(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.EqualFold(src, dst) {
		return nil // already running from the install location
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if _, err := os.Stat(dst); err == nil {
		os.Remove(dst + ".old")
		os.Rename(dst, dst+".old")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func taskXML(exe, args, user string) string {
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Tray icon that shows and switches Dolby Audio on the Shokz OpenFit 2+ earbuds (Ctrl+Q toggles).</Description>
    <URI>\` + taskName + `</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + xmlEscape(user) + `</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + xmlEscape(user) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEscape(exe) + `</Command>
      <Arguments>` + xmlEscape(args) + `</Arguments>
    </Exec>
  </Actions>
</Task>`
}

// writeUTF16 writes content as UTF-16 with a byte order mark: schtasks is
// reliable about UTF-16 XML and unreliable about UTF-8.
func writeUTF16(path, content string) error {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	for _, u := range utf16.Encode([]rune(content)) {
		binary.Write(&buf, binary.LittleEndian, u)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// stopTray ends a tray the task started, so its exe can be replaced. A
// missing or already-stopped task reports an error not worth showing.
func stopTray() {
	exec.Command("schtasks", "/End", "/TN", taskName).Run()
}

// install copies the exe to appDir, registers the logon task and starts it.
func install(stdout, stderr io.Writer) int {
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "budbar: install: "+format+"\n", args...)
		return 1
	}
	// Order matters: stop the old tray before replacing its exe, and start
	// the new one only once the task is registered.
	stopTray()
	if err := os.MkdirAll(appDir(), 0o755); err != nil {
		return fail("%v", err)
	}
	exe := installedExePath()
	if err := copySelf(exe); err != nil {
		return fail("copying to %s: %v", exe, err)
	}
	const args = "tray"
	xmlPath := filepath.Join(appDir(), "task.xml")
	if err := writeUTF16(xmlPath, taskXML(exe, args, currentUser())); err != nil {
		return fail("writing task xml: %v", err)
	}
	if out, err := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", xmlPath, "/F").CombinedOutput(); err != nil {
		return fail("schtasks /Create: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("schtasks", "/Run", "/TN", taskName).CombinedOutput(); err != nil {
		return fail("schtasks /Run: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(stdout, "installed  %s\narguments  %s\nlog        %s\ntask       %s (starts the tray at logon; started now)\n",
		exe, args, logPath(), taskName)
	return 0
}

// uninstall stops the tray and removes the task. The exe and log stay.
func uninstall(stdout, stderr io.Writer) int {
	// End before Delete: deleting a task does not reliably stop the process
	// it started.
	stopTray()
	if out, err := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "budbar: uninstall: schtasks /Delete: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	fmt.Fprintf(stdout, "removed task %s\nexe and log left in %s\n", taskName, appDir())
	return 0
}
