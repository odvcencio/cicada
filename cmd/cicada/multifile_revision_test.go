package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiFileFixPreservesConcurrentManifestEdit(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "parts.cicada")
	manifestPath := filepath.Join(root, "cicada.mod")
	manifest := []byte("project score\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\n")
	partSource := []byte("track bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\n")
	for path, source := range map[string][]byte{
		manifestPath: manifest,
		entry:        []byte("scene verse { bass = pulse }\nsong { verse }\n"),
		part:         partSource,
	} {
		if err := os.WriteFile(path, source, 0644); err != nil {
			t.Fatal(err)
		}
	}
	editedManifest := append(bytes.Clone(manifest), []byte("author \"Concurrent editor\"\n")...)
	edited := false
	err := fixCommandWithWriter([]string{entry, "--all"}, func(path string, source []byte, mode os.FileMode) error {
		if err := writeFixedScore(path, source, mode); err != nil {
			return err
		}
		edited = true
		return os.WriteFile(manifestPath, editedManifest, 0644)
	})
	if !edited {
		t.Fatal("test did not reach a source write")
	}
	if err == nil || !strings.Contains(err.Error(), "manifest changed during fix") {
		t.Errorf("expected a manifest revision conflict, got %v", err)
	}
	current, readErr := os.ReadFile(manifestPath)
	if readErr != nil || !bytes.Equal(current, editedManifest) {
		t.Errorf("concurrent manifest edit was lost: %v %q", readErr, current)
	}
	current, readErr = os.ReadFile(part)
	if readErr != nil || !bytes.Equal(current, partSource) {
		t.Errorf("source migration was not rolled back: %v %q", readErr, current)
	}
}

func TestMultiFileFixPreservesManifestEditDuringPublication(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "in-place"
		if replace {
			name = "rename"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			entry := filepath.Join(root, "main.cicada")
			part := filepath.Join(root, "parts.cicada")
			manifestPath := filepath.Join(root, "cicada.mod")
			manifest := []byte("project score\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\n")
			partSource := []byte("track bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\n")
			for path, source := range map[string][]byte{
				manifestPath: manifest,
				entry:        []byte("scene verse { bass = pulse }\nsong { verse }\n"),
				part:         partSource,
			} {
				if err := os.WriteFile(path, source, 0644); err != nil {
					t.Fatal(err)
				}
			}
			editedManifest := append(bytes.Clone(manifest), []byte("author \"Late editor\"\n")...)
			reached := false
			err := fixCommandWithHooks([]string{entry, "--all"}, nil, func() {
				reached = true
				path := manifestPath
				if replace {
					path = filepath.Join(root, "editor-save.tmp")
				}
				if err := os.WriteFile(path, editedManifest, 0644); err != nil {
					t.Fatal(err)
				}
				if replace {
					if err := os.Rename(path, manifestPath); err != nil {
						t.Fatal(err)
					}
				}
			})
			if !reached {
				t.Fatal("test did not reach staged manifest publication")
			}
			if err == nil || !strings.Contains(err.Error(), "manifest changed during fix") {
				t.Errorf("expected a manifest publication conflict, got %v", err)
			}
			current, readErr := os.ReadFile(manifestPath)
			if readErr != nil || !bytes.Equal(current, editedManifest) {
				t.Errorf("late manifest edit was lost: %v %q", readErr, current)
			}
			current, readErr = os.ReadFile(part)
			if readErr != nil || !bytes.Equal(current, partSource) {
				t.Errorf("source migration was not rolled back: %v %q", readErr, current)
			}
		})
	}
}

func TestFixRevisionWritePreservesConcurrentSourceEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	fixed := []byte("migrated source\n")
	before := []byte("original source\n")
	edited := []byte("editor source\n")
	if err := os.WriteFile(path, fixed, 0640); err != nil {
		t.Fatal(err)
	}
	err := writeFixedRevision(path, fixed, before, 0640, func() {
		if err := os.WriteFile(path, edited, 0640); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("expected a revision conflict during rollback publication")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, edited) {
		t.Fatalf("rollback lost editor source: %v %q", err, current)
	}
}

func TestMultiFileFixRollbackIncludesFailedWriter(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "parts.cicada")
	manifestPath := filepath.Join(root, "cicada.mod")
	manifest := []byte("project score\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\n")
	partSource := []byte("track bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\n")
	for path, source := range map[string][]byte{manifestPath: manifest, entry: []byte("scene verse { bass = pulse }\nsong { verse }\n"), part: partSource} {
		if err := os.WriteFile(path, source, 0644); err != nil {
			t.Fatal(err)
		}
	}
	err := fixCommandWithWriter([]string{entry, "--all"}, func(path string, source []byte, mode os.FileMode) error {
		if err := writeFixedScore(path, source, mode); err != nil {
			return err
		}
		return fmt.Errorf("injected post-publication error")
	})
	if err == nil || !strings.Contains(err.Error(), "injected post-publication error") {
		t.Fatalf("writer error: %v", err)
	}
	for path, want := range map[string][]byte{part: partSource, manifestPath: manifest} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("failed writer was not rolled back: %v %q", err, got)
		}
	}
}

func TestMultiFileFixPreservesManifestEditWithoutEditionUpgrade(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "parts.cicada")
	manifestPath := filepath.Join(root, "cicada.mod")
	manifest := []byte("project score\ncicada 2\nentry \"main.cicada\"\nsource \"parts.cicada\"\n")
	partSource := []byte("cicada 1\ntrack bass acid { bus = music }\npattern pulse acid steps=4 { 1 . 5 . }\n")
	for path, source := range map[string][]byte{manifestPath: manifest, entry: []byte("scene verse { bass = pulse }\nsong { verse }\n"), part: partSource} {
		if err := os.WriteFile(path, source, 0644); err != nil {
			t.Fatal(err)
		}
	}
	edited := append(bytes.Clone(manifest), []byte("author \"Edition two editor\"\n")...)
	reached := false
	err := fixCommandWithWriter([]string{entry, "--all"}, func(path string, source []byte, mode os.FileMode) error {
		if err := writeFixedScore(path, source, mode); err != nil {
			return err
		}
		reached = true
		return os.WriteFile(manifestPath, edited, 0644)
	})
	if !reached {
		t.Fatal("test did not migrate a source in the edition-two project")
	}
	if err == nil || !strings.Contains(err.Error(), "manifest changed during fix") {
		t.Fatalf("manifest conflict: %v", err)
	}
	for path, want := range map[string][]byte{manifestPath: edited, part: partSource} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("no-upgrade rollback lost edit: %v %q", err, got)
		}
	}
}

func TestMultiFileFixRetainsLateOpenWriterRecovery(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "parts.cicada")
	manifestPath := filepath.Join(root, "cicada.mod")
	manifest := []byte("project score\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\n")
	partSource := []byte("track bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\n")
	for path, source := range map[string][]byte{manifestPath: manifest, entry: []byte("scene verse { bass = pulse }\nsong { verse }\n"), part: partSource} {
		if err := os.WriteFile(path, source, 0644); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := os.OpenFile(part, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := fixCommand([]string{entry, "--all"}); err != nil {
		t.Fatal(err)
	}
	edited := append(bytes.Clone(partSource), []byte("// late open writer save\n")...)
	if err := writer.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(edited); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	for _, item := range entries {
		data, err := os.ReadFile(filepath.Join(root, item.Name()))
		if err == nil && bytes.Equal(data, edited) {
			retained = true
		}
	}
	if !retained {
		t.Fatal("late write through the displaced file handle was lost")
	}
	err = fixCommand([]string{entry, "--all", "--check"})
	if err == nil || !strings.Contains(err.Error(), "external write") {
		t.Fatalf("late recovery must block even an otherwise idempotent migration: %v", err)
	}
}
