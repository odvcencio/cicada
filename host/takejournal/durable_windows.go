package takejournal

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockJournal(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

// Windows does not support FlushFileBuffers on directory handles. File contents
// are flushed; directory durability depends on the host filesystem.
func syncDirectory(f *os.File) error { return nil }
