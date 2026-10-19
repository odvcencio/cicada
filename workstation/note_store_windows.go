package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockNoteStore(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{})
}
func unlockNoteStore(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}

// Windows flushes the file contents but does not support syncing directory handles.
func syncNoteDirectory(f *os.File) error { return nil }
