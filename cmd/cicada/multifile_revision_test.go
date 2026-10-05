package main

import (
	"bytes"
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
