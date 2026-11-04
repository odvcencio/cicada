package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleKeepsMidiMappingsButNotLocalDevices(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "main.cicada")
	source := `cicada 2
track bass acid {}
midi { port keys keys cc 74 -> bass.cutoff }
pattern phrase { 1 }
scene main { bass = phrase }
song { main }
`
	if err := os.WriteFile(score, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cicada.local"), []byte("port keys = \"Private keyboard\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "song.zip")
	if err := bundleCommand([]string{score, path}); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	kept := false
	for _, file := range archive.File {
		if filepath.Base(file.Name) == "cicada.local" {
			t.Fatal("bundle contains local device bindings")
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("Private keyboard")) {
			t.Fatalf("bundle leaks device name through %s", file.Name)
		}
		if file.Name == "main.cicada" && bytes.Contains(data, []byte("keys cc 74 -> bass.cutoff")) {
			kept = true
		}
	}
	if !kept {
		t.Fatal("bundle lost portable MIDI mapping")
	}
}
