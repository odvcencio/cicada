//go:build windows

package main

// The Windows side of the desktop host in main_desktop.go: the calls that
// touch Win32 or Windows conventions directly.

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

// defaultDataDir is the folder for settings and logs when --data-dir is not
// set: %LOCALAPPDATA%\Cicada Studio.
func defaultDataDir() (string, error) {
	base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Cicada Studio"), nil
}

// webViewDataDir is the WebView2 profile folder inside dataDir.
func webViewDataDir(dataDir string) string {
	return filepath.Join(dataDir, "WebView2")
}

// hideConsole keeps the helper processes the host starts from opening console
// windows (CREATE_NO_WINDOW).
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// showError shows a native error box. The host is a GUI-subsystem program, so
// there is no console to print to.
func showError(title, message string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(message)
	const mbIconError, mbSetForeground = 0x10, 0x10000
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError|mbSetForeground)
}
