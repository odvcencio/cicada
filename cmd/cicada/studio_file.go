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
	displacedPath, err := studioSwap(path, stagePath)
	if err != nil {
		if errors.Is(err, errStudioSwapUnavailable) {
			return false, "", errStudioSwapUnavailable
		}
		if displacedPath != "" {
			return false, displacedPath, fmt.Errorf("score replacement failed; displaced score preserved at %s: %w", displacedPath, err)
		}
		return false, "", err
	}
	displaced, err := os.ReadFile(displacedPath)
	if err == nil && studioRevision(displaced) == expected {
		if displacedPath != stagePath {
			_ = os.Remove(displacedPath)
		}
		return true, "", nil
	}
	// The displaced path holds another editor's score. Never delete it unless
	// the exchange back succeeds. If a second writer intervenes, retain that
	// version for recovery rather than silently discarding it.
	interveningPath, rollbackErr := studioSwap(path, displacedPath)
	if rollbackErr != nil {
		if displacedPath == stagePath {
			removeStage = false
		}
		return false, displacedPath, fmt.Errorf("score changed during commit; rollback failed: %w", rollbackErr)
	}
	intervening, readErr := os.ReadFile(interveningPath)
	if readErr != nil || !bytes.Equal(intervening, updated) {
		if interveningPath == stagePath {
			removeStage = false
		}
		return false, interveningPath, fmt.Errorf("score changed during commit; another version was preserved at %s", interveningPath)
	}
	if interveningPath != stagePath {
		_ = os.Remove(interveningPath)
	}
	return false, "", nil
}
