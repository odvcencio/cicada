//go:build darwin

package main

// The macOS side of the desktop host in main_desktop.go: the calls that depend
// on macOS conventions rather than on the GoSX desktop package.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"m31labs.dev/gosx/desktop"
)

// defaultDataDir is the folder for settings and logs when --data-dir is not
// set: ~/Library/Application Support/Cicada Studio. os.UserCacheDir would
// point at ~/Library/Caches, which the system may clear.
func defaultDataDir() (string, error) {
	base, err := os.UserConfigDir() // ~/Library/Application Support on macOS
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Cicada Studio"), nil
}

// webViewDataDir is empty on macOS: WKWebView keeps its website data in the
// default store keyed by the bundle identifier, and the GoSX darwin backend
// ignores Options.UserDataDir.
func webViewDataDir(string) string { return "" }

// hideConsole does nothing on macOS: a helper process started by an app has no
// console window to hide.
func hideConsole(*exec.Cmd) {}

// showError reports a failure that stops an action. It always writes to
// stderr, which is visible when the host runs from a terminal, and then asks
// GoSX for a native message box. A backend without one (the darwin backend
// before its NSAlert slice) returns desktop.ErrUnsupported and the stderr line
// stands alone.
func showError(title, message string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
	_, _ = desktop.ShowMessage(desktop.MessageOptions{Title: title, Text: message, Kind: desktop.MessageError})
}
