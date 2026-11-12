//go:build !unix

package main

import "os"

// peakRSS is not measured where the host does not report it.
func peakRSS(*os.ProcessState) int64 { return 0 }
