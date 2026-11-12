//go:build windows

package main

import (
	"errors"
	"syscall"
)

// isNoSpace reports whether err says the disk is full (ERROR_HANDLE_DISK_FULL
// or ERROR_DISK_FULL).
func isNoSpace(err error) bool {
	return errors.Is(err, syscall.Errno(39)) || errors.Is(err, syscall.Errno(112))
}
