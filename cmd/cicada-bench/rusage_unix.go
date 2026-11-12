//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS returns the largest resident set of a finished process in bytes.
// Linux reports it in kibibytes and macOS in bytes.
func peakRSS(state *os.ProcessState) int64 {
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return int64(usage.Maxrss)
	}
	return int64(usage.Maxrss) * 1024
}
