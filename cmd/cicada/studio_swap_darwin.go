package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// RENAME_SWAP preserves the displaced inode, including any open editor
// handles, so the shared revision check and recovery logic can detect races.
func studioSwap(path, replacement string) (string, error) {
	if err := unix.RenamexNp(path, replacement, unix.RENAME_SWAP); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOSYS) {
			return "", errStudioSwapUnavailable
		}
		return "", &os.LinkError{Op: "rename-swap", Old: path, New: replacement, Err: err}
	}
	return replacement, nil
}
