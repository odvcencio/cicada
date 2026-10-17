//go:build !windows && !darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// The desktop host needs a GoSX desktop backend, which exists for Windows
// (main_desktop.go, host_windows.go) and macOS (main_desktop.go,
// host_darwin.go). Elsewhere, run `cicada studio` and open the printed address
// in a browser.
func main() {
	fmt.Fprintln(os.Stderr, "Cicada Studio desktop currently runs on Windows and macOS; run `cicada studio <score.cicada>` instead.")
	os.Exit(1)
}

func hideConsole(*exec.Cmd) {}
