//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// The desktop host needs the GoSX Windows backend. Elsewhere, run
// `cicada studio` and open the printed address in a browser.
func main() {
	fmt.Fprintln(os.Stderr, "Cicada Studio desktop currently runs on Windows; run `cicada studio <score.cicada>` instead.")
	os.Exit(1)
}

func hideConsole(*exec.Cmd) {}
