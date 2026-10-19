//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockNoteStore(f *os.File) error     { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlockNoteStore(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func syncNoteDirectory(f *os.File) error { return f.Sync() }
