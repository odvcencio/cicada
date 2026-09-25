package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errStudioSwapUnavailable = errors.New("atomic score exchange is unavailable on this filesystem; edit the score in your text editor")

// studioWriteIfRevision exchanges an already validated score with a staged
// file, then checks the exact version displaced by that exchange. A pathname
// replacement by another editor between validation and commit is detected.
// On conflict the displaced version is exchanged back into place.
func studioWriteIfRevision(path string, updated []byte, mode os.FileMode, expected string, beforeSwap func()) (bool, string, error) {
	stage, err := os.CreateTemp(filepath.Dir(path), ".cicada-studio-*")
	if err != nil {
		return false, "", err
	}
	stagePath := stage.Name()
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.Remove(stagePath)
		}
	}()
	if err := stage.Chmod(mode); err != nil {
		_ = stage.Close()
		return false, "", err
	}
	if _, err := stage.Write(updated); err != nil {
		_ = stage.Close()
		return false, "", err
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close()
		return false, "", err
	}
	if err := stage.Close(); err != nil {
		return false, "", err
	}
	if beforeSwap != nil {
		beforeSwap()
	}
	if err := studioSwap(path, stagePath); err != nil {
		if errors.Is(err, errStudioSwapUnavailable) {
			return false, "", errStudioSwapUnavailable
		}
		return false, "", err
	}
	displaced, err := os.ReadFile(stagePath)
	if err == nil && studioRevision(displaced) == expected {
		return true, "", nil
	}
	// The staged path currently holds another editor's score. Never delete it
	// unless the exchange back succeeds. If a second writer intervenes, retain
	// that version for recovery rather than silently discarding it.
	if rollbackErr := studioSwap(path, stagePath); rollbackErr != nil {
		removeStage = false
		return false, stagePath, fmt.Errorf("score changed during commit; rollback failed: %w", rollbackErr)
	}
	intervening, readErr := os.ReadFile(stagePath)
	if readErr != nil || !bytes.Equal(intervening, updated) {
		removeStage = false
		return false, stagePath, fmt.Errorf("score changed during commit; another version was preserved at %s", stagePath)
	}
	return false, "", nil
}
