package main

import "golang.org/x/sys/windows/registry"

// taskbarDark reports whether the taskbar uses the dark theme. Windows keeps
// it in SystemUsesLightTheme (0 = dark); a missing value counts as light.
func taskbarDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("SystemUsesLightTheme")
	return err == nil && v == 0
}
