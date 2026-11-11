//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// isNoSpace reports whether err says the disk is full.
func isNoSpace(err error) bool { return errors.Is(err, syscall.ENOSPC) }
