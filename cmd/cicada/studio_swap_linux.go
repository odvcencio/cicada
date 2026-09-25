//go:build linux

package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

// studioSwap atomically exchanges the score pathname with a staged file. After
// success, replacement holds the score version displaced by the transaction.
func studioSwap(path, replacement string) error {
	err := unix.Renameat2(unix.AT_FDCWD, replacement, unix.AT_FDCWD, path, unix.RENAME_EXCHANGE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return errStudioSwapUnavailable
	}
	return err
}
