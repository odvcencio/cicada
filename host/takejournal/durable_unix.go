//go:build !windows

package takejournal

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockJournal(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }

// unlockJournal releases the lock explicitly. A flock belongs to the open file
// description, which a concurrently forked child shares until it execs, so
// closing our descriptor alone can leave the journal locked for a moment.
func unlockJournal(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func syncDirectory(f *os.File) error { return f.Sync() }
