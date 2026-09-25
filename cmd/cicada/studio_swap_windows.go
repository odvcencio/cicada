//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// studioSwap replaces the score and asks Windows to preserve the displaced
// file at a sibling pathname. ReplaceFileW keeps its backup on the same volume.
func studioSwap(path, replacement string) (string, error) {
	backupFile, err := os.CreateTemp(filepath.Dir(path), ".cicada-displaced-*")
	if err != nil {
		return "", err
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backup)
		return "", err
	}
	if err := os.Remove(backup); err != nil {
		return "", err
	}
	replaced, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	newFile, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return "", err
	}
	backupName, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return "", err
	}
	result, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(replaced)),
		uintptr(unsafe.Pointer(newFile)),
		uintptr(unsafe.Pointer(backupName)),
		0, 0, 0,
	)
	runtime.KeepAlive(replaced)
	runtime.KeepAlive(newFile)
	runtime.KeepAlive(backupName)
	if result == 0 {
		if _, statErr := os.Stat(backup); statErr == nil {
			// ReplaceFileW can fail after moving the original to its backup.
			// Return the path so the caller keeps that version for recovery.
			return backup, fmt.Errorf("ReplaceFileW: %w", callErr)
		}
		return "", fmt.Errorf("ReplaceFileW: %w", callErr)
	}
	return backup, nil
}
