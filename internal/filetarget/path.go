// Package filetarget resolves physical paths before file publication.
package filetarget

import (
	"fmt"
	"os"
	"path/filepath"
)

type Mode uint8

const (
	Existing Mode = iota
	MayCreate
)

// Resolve returns an absolute physical path. Existing requires the target to
// exist. MayCreate resolves the nearest existing ancestor and appends missing
// components without creating them; the caller retains its creation policy.
// Dangling links are occupied paths, so neither mode treats them as missing.
func Resolve(path string, mode Mode) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if mode == Existing {
		return filepath.EvalSymlinks(absolute)
	}
	if mode != MayCreate {
		return "", fmt.Errorf("unknown file target resolution mode %d", mode)
	}
	var missing []string
	for {
		if _, err := os.Lstat(absolute); err == nil {
			target, err := filepath.EvalSymlinks(absolute)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				target = filepath.Join(target, missing[i])
			}
			return target, nil
		} else if !os.IsNotExist(err) || filepath.Dir(absolute) == absolute {
			return "", err
		}
		missing = append(missing, filepath.Base(absolute))
		absolute = filepath.Dir(absolute)
	}
}

// Create resolves existing parent directories and preserves missing components
// and the final component, leaving parent creation to the caller.
// Exclusive creators must still refuse an occupied leaf, including a symlink.
func Create(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := Resolve(filepath.Dir(absolute), MayCreate)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

// Write resolves an existing target or the ancestors of a new file. A dangling
// symlink is an occupied leaf and must not be replaced as if it were absent.
func Write(path string) (string, error) {
	return Resolve(path, MayCreate)
}
