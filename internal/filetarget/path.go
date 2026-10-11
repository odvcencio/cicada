// Package filetarget resolves physical paths before file publication.
package filetarget

import (
	"os"
	"path/filepath"
)

// Resolve returns the absolute, symlink-resolved path of an existing target.
// Use it before reading source or discovering the target's project.
func Resolve(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// Create resolves the parent directory while preserving the final component.
// Exclusive creators must still refuse an occupied leaf, including a symlink.
func Create(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := Resolve(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

// Write resolves an existing target or the parent of a new file. A dangling
// symlink is an occupied leaf and must not be replaced as if it were absent.
func Write(path string) (string, error) {
	target, err := Resolve(path)
	if err == nil || !os.IsNotExist(err) {
		return target, err
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return "", err
	}
	return Create(path)
}
