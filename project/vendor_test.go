package project

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func userVendorFixture(t *testing.T) (string, string, *Sources) {
	t.Helper()
	root, user := libraryFixture(t)
	if err := os.Rename(filepath.Join(root, "lib", "demo"), filepath.Join(user, "demo")); err != nil {
		t.Fatal(err)
	}
	installLibrary(t, user, "demo/phrases", "phrase hook { 1 . 5 . }\n")
	libraryWrite(t, user, "demo/tone/tone.cicada", "import \"demo/phrases\"\n"+libraryVoice)
	return root, user, pinLibraryFixture(t, root)
}

func TestVendorTransitiveCopiesAndPins(t *testing.T) {
	root, user, sources := userVendorFixture(t)
	// Hash all audio files, including nested files not referenced by a sampler.
	libraryWrite(t, user, "demo/tone/audio/nested/extra.wav", "hashed audio bytes")
	sources = pinLibraryFixture(t, root)
	changes, err := sources.VendorLibraries(root)
	if err != nil || len(changes) != 2 {
		t.Fatalf("vendor: %v %v", changes, err)
	}
	for name, before := range sources.Libraries {
		after, err := InspectLibrary(root, name)
		if err != nil || after.Kind != "project" || after.SHA256 != before.SHA256 {
			t.Fatalf("vendor %s: %v %+v", name, err, after)
		}
	}
	if err := os.RemoveAll(user); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := loaded.Parse(); len(ds) != 0 {
		t.Fatal(ds)
	}
	if changes, err := loaded.VendorLibraries(root); err != nil || len(changes) != 0 {
		t.Fatalf("repeat: %v %v", changes, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "lib", "demo", "tone", "audio", "nested", "extra.wav"))
	if err != nil || string(data) != "hashed audio bytes" {
		t.Fatalf("audio copy: %s %v", data, err)
	}
}

func TestVendorRefusesCollisionAndChangedSource(t *testing.T) {
	for _, kind := range []string{"collision", "hash-mismatch", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, user, sources := userVendorFixture(t)
			dir := filepath.Join(root, "lib", "demo", "tone")
			switch kind {
			case "collision":
				installLibrary(t, filepath.Join(root, "lib"), "demo/tone", libraryVoice+"// different library\n")
			case "hash-mismatch":
				libraryWrite(t, user, "demo/tone/tone.cicada", libraryVoice+"// unexpected change\n")
			case "symlink":
				outside := t.TempDir()
				libraryWrite(t, outside, "sentinel", "unchanged")
				if err := os.Symlink(outside, dir); err != nil {
					t.Skip(err)
				}
			}
			sum, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
			if _, err := sources.VendorLibraries(root); err == nil {
				t.Fatal("invalid vendor succeeded")
			}
			after, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
			if !bytes.Equal(sum, after) {
				t.Fatal("failed vendoring changed pins")
			}
			if kind == "collision" {
				data, _ := os.ReadFile(filepath.Join(dir, "tone.cicada"))
				if !strings.Contains(string(data), "different library") {
					t.Fatal("collision overwritten")
				}
			}
			if _, err := os.Stat(filepath.Join(root, "lib", "demo", "phrases")); !os.IsNotExist(err) {
				t.Fatal("partial library published")
			}
		})
	}
}

func TestVendorRollsBackPartialPublication(t *testing.T) {
	for _, kind := range []string{"second-rename", "sum-rename", "copy-hash"} {
		t.Run(kind, func(t *testing.T) {
			root, _, sources := userVendorFixture(t)
			sum, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
			calls := 0
			_, err := sources.vendorLibraries(root, func(from, to string) error {
				calls++
				if (kind == "second-rename" && calls == 2) || (kind == "sum-rename" && to == "cicada.sum") {
					return errors.New("injected publication failure")
				}
				if err := os.Rename(filepath.Join(root, from), filepath.Join(root, to)); err != nil {
					return err
				}
				if kind == "copy-hash" && calls == 1 {
					libraryWrite(t, root, filepath.ToSlash(filepath.Join(to, "tone.cicada")), "phrase hook { 3 . }\n")
				}
				return nil
			})
			if err == nil {
				t.Fatal("fault was not detected")
			}
			after, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
			if !bytes.Equal(sum, after) {
				t.Fatal("failed publication changed pins")
			}
			for name := range sources.Libraries {
				if _, err := os.Stat(filepath.Join(root, "lib", filepath.FromSlash(name))); !os.IsNotExist(err) {
					t.Fatalf("partial library retained: %s", name)
				}
			}
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".cicada-vendor") {
					t.Fatal("transaction debris retained")
				}
			}
		})
	}
}

func TestVendorAcceptsIdenticalCopyAndIgnoresChangedUserAfterPinning(t *testing.T) {
	root, user, sources := userVendorFixture(t)
	for name, lib := range sources.Libraries {
		for path, data := range lib.content {
			libraryWrite(t, filepath.Join(root, "lib", filepath.FromSlash(name)), path, string(data))
		}
	}
	loaded, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.VendorLibraries(root); err != nil {
		t.Fatal(err)
	}
	libraryWrite(t, user, "demo/tone/tone.cicada", "instrument other { voice mono { out = sine(pitch) } }\n")
	loaded, err = ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := loaded.Parse(); len(ds) != 0 {
		t.Fatal(ds)
	}
}

func TestVendorAcceptsSameProjectDirectoryAlias(t *testing.T) {
	root, _, sources := userVendorFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip(err)
	}
	if _, err := sources.VendorLibraries(alias); err != nil {
		t.Fatalf("same-project alias rejected: %v", err)
	}
	loaded, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := loaded.Parse(); len(ds) != 0 {
		t.Fatal(ds)
	}
}

func TestVendorRollbackPreservesConcurrentEdits(t *testing.T) {
	for _, kind := range []string{"addition", "edit"} {
		t.Run(kind, func(t *testing.T) {
			root, _, sources := userVendorFixture(t)
			var installed, filename string
			_, err := sources.vendorLibraries(root, func(from, to string) error {
				if installed != "" {
					return errors.New("injected publication failure")
				}
				if err := os.Rename(filepath.Join(root, from), filepath.Join(root, to)); err != nil {
					return err
				}
				installed = to
				filename = "notes.txt"
				if kind == "edit" {
					filename = "tone.cicada"
				}
				libraryWrite(t, root, filepath.ToSlash(filepath.Join(to, filename)), "concurrent user content")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "recovery copy retained at") {
				t.Fatalf("missing recovery report: %v", err)
			}
			matches, globErr := filepath.Glob(filepath.Join(root, ".cicada-recovery-*", "demo", "*", filename))
			if globErr != nil || len(matches) != 1 {
				t.Fatalf("recovery copy: %v %v", matches, globErr)
			}
			data, readErr := os.ReadFile(matches[0])
			if readErr != nil || string(data) != "concurrent user content" {
				t.Fatalf("lost concurrent content: %s %v", data, readErr)
			}
		})
	}
}

func TestLibraryUpdateCannotPublishDuringVendoring(t *testing.T) {
	root, _, sources := userVendorFixture(t)
	attempted := false
	_, err := sources.vendorLibraries(root, func(from, to string) error {
		if to == "cicada.sum" {
			attempted = true
			if _, err := sources.WriteLibraryUpdates("demo/tone"); err == nil || !strings.Contains(err.Error(), "cannot lock library pins") {
				t.Fatalf("concurrent sum writer was not excluded: %v", err)
			}
		}
		return os.Rename(filepath.Join(root, from), filepath.Join(root, to))
	})
	if err != nil || !attempted {
		t.Fatalf("vendor publication: %v, attempted=%v", err, attempted)
	}
	loaded, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.WriteLibraryUpdates("demo/tone"); err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	for name, pin := range mustReadLibraryPins(t, loaded) {
		if pin.Kind != "project" {
			t.Fatalf("lost vendor pin %s: %+v", name, pin)
		}
	}
}

func mustReadLibraryPins(t *testing.T, sources *Sources) map[string]LibraryPin {
	t.Helper()
	pins, err := sources.readSum()
	if err != nil {
		t.Fatal(err)
	}
	return pins
}

func TestVendoredProjectPinIgnoresInvalidUserLocation(t *testing.T) {
	for _, kind := range []string{"library-file", "root-file"} {
		t.Run(kind, func(t *testing.T) {
			root, user, sources := userVendorFixture(t)
			if _, err := sources.VendorLibraries(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(user, "demo", "tone")
			if kind == "root-file" {
				path = user
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("unused location"), 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := ReadSources(filepath.Join(root, "main.cicada"), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, ds := loaded.Parse(); len(ds) != 0 {
				t.Fatal(ds)
			}
		})
	}
}
