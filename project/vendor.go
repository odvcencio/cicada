package project

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// VendorLibraries installs every imported non-standard library in target/lib.
// Existing libraries must have identical hashes. Source pins are verified;
// only resolution kinds change. Copies are staged and verified before rename,
// and newly installed directories are removed if publication fails. Target pins
// are written last, preserving unrelated pins in an existing target project.
func (s *Sources) VendorLibraries(target string) ([]string, error) {
	return s.vendorLibraries(target, nil)
}

func (s *Sources) vendorLibraries(target string, rename func(string, string) error) (changes []string, err error) {
	if len(s.Libraries) == 0 {
		return nil, nil
	}
	if ds := s.verifyLibraries(); len(ds) != 0 {
		return nil, &SourceError{Diagnostic: ds[0]}
	}
	dst, err := os.OpenRoot(target)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	lock, err := dst.OpenFile(".cicada-vendor.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock library vendoring: %w", err)
	}
	lock.Close()
	defer dst.Remove(".cicada-vendor.lock")
	if rename == nil {
		rename = dst.Rename
	}
	oldSum, err := dst.ReadFile("cicada.sum")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pins, err := ParseLibrarySum(oldSum)
	if err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	stage := ".cicada-vendor-" + hex.EncodeToString(id[:])
	if err := dst.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	defer dst.RemoveAll(stage)
	names := make([]string, 0, len(s.Libraries))
	for name := range s.Libraries {
		names = append(names, name)
	}
	sort.Strings(names)
	var pending, installed []string
	staged := map[string]string{}
	defer func() {
		if err != nil {
			for i := len(installed) - 1; i >= 0; i-- {
				err = errors.Join(err, dst.RemoveAll(filepath.Join("lib", filepath.FromSlash(installed[i]))))
			}
			changes = nil
		}
	}()
	for _, name := range names {
		lib := s.Libraries[name]
		pin := lib.LibraryPin
		if lib.Kind != "std" {
			// Re-read disk rather than copying stale editor or loader buffers.
			src, e := os.OpenRoot(lib.Root)
			if e != nil {
				return nil, e
			}
			current, e := readLibrary(name, lib.Kind, lib.Root, src.FS(), nil)
			src.Close()
			if e != nil {
				return nil, e
			}
			if current.LibraryPin != lib.LibraryPin {
				return nil, fmt.Errorf("CICADA-LIB-HASH: library %s changed during vendoring", name)
			}
			path := filepath.Join("lib", filepath.FromSlash(name))
			if _, e := dst.Lstat(path); e == nil {
				if e := verifyVendor(dst, path, lib); e != nil {
					return nil, fmt.Errorf("vendoring refuses different library at %s: %w", name, e)
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return nil, e
			} else {
				copyPath := filepath.Join(stage, fmt.Sprintf("library-%d", len(pending)))
				for file, data := range current.content {
					to := filepath.Join(copyPath, filepath.FromSlash(file))
					if e := dst.MkdirAll(filepath.Dir(to), 0755); e != nil {
						return nil, e
					}
					if e := writeVendorFile(dst, to, data); e != nil {
						return nil, e
					}
				}
				if e := verifyVendor(dst, copyPath, lib); e != nil {
					return nil, e
				}
				pending = append(pending, name)
				staged[name] = copyPath
			}
			pin.Kind = "project"
		}
		if previous, found := pins[name]; found && previous != pin && target != s.Root {
			return nil, fmt.Errorf("vendoring refuses conflicting target pin for %s", name)
		}
		if pins[name] != pin {
			changes = append(changes, fmt.Sprintf("%s: %s -> %s sha256:%s", name, lib.Kind, pin.Kind, pin.SHA256))
			pins[name] = pin
		}
	}
	newSum := FormatLibrarySum(pins)
	if err := writeVendorFile(dst, filepath.Join(stage, "cicada.sum"), newSum); err != nil {
		return nil, err
	}
	for _, name := range pending {
		to := filepath.Join("lib", filepath.FromSlash(name))
		if err := dst.MkdirAll(filepath.Dir(to), 0755); err != nil {
			return nil, err
		}
		// Never replace even an empty directory that appeared during staging.
		if _, err := dst.Lstat(to); err == nil {
			return nil, fmt.Errorf("vendoring destination appeared: %s", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := rename(staged[name], to); err != nil {
			return nil, err
		}
		installed = append(installed, name)
		if err := verifyVendor(dst, to, s.Libraries[name]); err != nil {
			return nil, err
		}
	}
	latest, err := dst.ReadFile("cicada.sum")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !bytes.Equal(oldSum, latest) {
		return nil, fmt.Errorf("library pins changed during vendoring; retry")
	}
	if !bytes.Equal(oldSum, newSum) {
		if err := rename(filepath.Join(stage, "cicada.sum"), "cicada.sum"); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

func verifyVendor(root *os.Root, path string, expected *Library) error {
	files, err := root.OpenRoot(path)
	if err != nil {
		return err
	}
	defer files.Close()
	actual, err := readLibrary(expected.Path, "project", path, files.FS(), nil)
	if err != nil {
		return err
	}
	if actual.Manifest.Library != expected.Path || actual.SHA256 != expected.SHA256 {
		return fmt.Errorf("CICADA-LIB-HASH: copied library %s differs", expected.Path)
	}
	return nil
}

func writeVendorFile(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
