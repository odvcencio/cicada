// Package filetarget keeps declared file identities separate from write targets.
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

// Target preserves the absolute logical name for loaders, overrides, diagnostics
// and edit options. Physical is only for publication, revision checks and
// confinement; substituting it during loading loses manifest source identity.
type Target struct {
	Logical  string
	Physical string
}

// Resolve returns the logical path and its physical target. Existing requires
// the target to exist. MayCreate resolves the nearest existing ancestor and
// appends missing components without creating them; the caller retains its
// creation policy.
// Dangling links are occupied paths, so neither mode treats them as missing.
func Resolve(path string, mode Mode) (Target, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Target{}, err
	}
	logical := absolute
	if mode == Existing {
		physical, err := filepath.EvalSymlinks(absolute)
		return Target{Logical: logical, Physical: physical}, err
	}
	if mode != MayCreate {
		return Target{}, fmt.Errorf("unknown file target resolution mode %d", mode)
	}
	var missing []string
	for {
		if _, err := os.Lstat(absolute); err == nil {
			target, err := filepath.EvalSymlinks(absolute)
			if err != nil {
				return Target{}, err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				target = filepath.Join(target, missing[i])
			}
			return Target{Logical: logical, Physical: target}, nil
		} else if !os.IsNotExist(err) || filepath.Dir(absolute) == absolute {
			return Target{}, err
		}
		missing = append(missing, filepath.Base(absolute))
		absolute = filepath.Dir(absolute)
	}
}

// Create resolves existing parent directories and preserves missing components
// and the final component, leaving parent creation to the caller.
// Exclusive creators must still refuse an occupied leaf, including a symlink.
func Create(path string) (Target, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Target{}, err
	}
	parent, err := Resolve(filepath.Dir(absolute), MayCreate)
	if err != nil {
		return Target{}, err
	}
	return Target{Logical: absolute, Physical: filepath.Join(parent.Physical, filepath.Base(absolute))}, nil
}

// Write resolves an existing target or the ancestors of a new file. A dangling
// symlink is an occupied leaf and must not be replaced as if it were absent.
func Write(path string) (Target, error) {
	return Resolve(path, MayCreate)
}
