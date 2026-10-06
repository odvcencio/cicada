//go:build !windows

package takejournal

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockJournal(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func syncDirectory(f *os.File) error { return f.Sync() }
