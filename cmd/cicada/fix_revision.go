package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Validate the displaced revision, including editor saves during staging/fsync.
// Keep displaced inodes linked so late writes through open handles are recoverable.
func writeFixedRevision(path string, before, after []byte, mode os.FileMode, beforeSwap func()) error {
	committed, recovery, err := studioWriteIfRevision(path, after, mode, studioRevision(before), beforeSwap)
	if err != nil {
		return err
	}
	if !committed {
		return fmt.Errorf("file was not published; saved version remains at %s", recovery)
	}
	return nil
}

// Probe only disposable files; never fall back to a check followed by rename.
func fixCheckExchange(dir string) error {
	first, err := os.CreateTemp(dir, ".cicada-fix-probe-*")
	if err != nil {
		return err
	}
	defer os.Remove(first.Name())
	if err := first.Close(); err != nil {
		return err
	}
	second, err := os.CreateTemp(dir, ".cicada-fix-probe-*")
	if err != nil {
		return err
	}
	defer os.Remove(second.Name())
	if err := second.Close(); err != nil {
		return err
	}
	displaced, err := studioSwap(first.Name(), second.Name())
	if displaced != "" {
		defer os.Remove(displaced)
	}
	if err != nil {
		return fmt.Errorf("multi-file fix requires atomic file exchange on %s: %w", filepath.Clean(dir), err)
	}
	return nil
}
