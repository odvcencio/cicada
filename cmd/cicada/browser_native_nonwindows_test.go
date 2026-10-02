//go:build (browser || browser_soak) && !windows

package main

import "fmt"

// The existing WSL PowerShell route is selected on non-Windows hosts. These
// stubs make accidental native API use fail instead of selecting another route.
func resizeNativeWindowsChromeWindow(_ string, _, _ int) error {
	return fmt.Errorf("native Windows Chrome resize requires a Windows test executable")
}

func captureNativeWindowsChromeScreenshot(_, _ string) error {
	return fmt.Errorf("native Windows Chrome screenshot requires a Windows test executable")
}
