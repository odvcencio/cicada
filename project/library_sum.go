package project

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// LockLibraryPins excludes concurrent pin writers until the returned release
// function is called. Editors must hold it while preparing and publishing pins.
func LockLibraryPins(directory string) (func(), error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	unlock, err := lockLibrarySum(root)
	if err != nil {
		root.Close()
		return nil, err
	}
	return func() { unlock(); root.Close() }, nil
}

func lockLibrarySum(root *os.Root) (func(), error) {
	lock, err := root.OpenFile(".cicada-vendor.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock library pins; another library operation may be active: %w", err)
	}
	if err := lock.Close(); err != nil {
		root.Remove(".cicada-vendor.lock")
		return nil, err
	}
	return func() { root.Remove(".cicada-vendor.lock") }, nil
}

// WriteLibraryUpdates holds the same lock as vendoring from reading pins
// through atomic publication, so concurrent commands cannot lose updates.
func (s *Sources) WriteLibraryUpdates(name string) ([]string, error) {
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	unlock, err := lockLibrarySum(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	before, err := root.ReadFile("cicada.sum")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	missing := errors.Is(err, os.ErrNotExist)
	data, changes, err := s.UpdateLibraries(name)
	if err != nil {
		return nil, err
	}
	if !missing && bytes.Equal(before, data) {
		return changes, nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	stage := ".cicada-sum-" + hex.EncodeToString(id[:])
	defer root.Remove(stage)
	if err := writeVendorFile(root, stage, data); err != nil {
		return nil, err
	}
	if err := root.Rename(stage, "cicada.sum"); err != nil {
		return nil, err
	}
	return changes, nil
}
