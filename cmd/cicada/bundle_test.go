package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/internal/vendortest"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestBundleVendorsAudioAndReportsExactFiles(t *testing.T) {
	root, user, _ := vendortest.Setup(t)
	audio := testwav.Bytes(48000, 1, 16, 4800, 1)
	vendortest.Write(t, user, "demo/tone/audio/nested/tone.wav", audio)
	declarations := fmt.Sprintf("asset wave \"audio/nested/tone.wav\" { sha256 = \"%x\" format = wav frames = 4800 rate = 48000Hz channels = 1 }\nsampler hit { asset = wave root = c3 mode = oneshot voices = 8 }\npattern melody { c3 . c3 . }\n", sha256.Sum256(audio))
	vendortest.Write(t, user, "demo/tone/parts/tone.cicada", []byte(declarations))
	vendortest.Write(t, root, "main.cicada", []byte("import \"demo/tone\"\ntrack lead tone.hit {}\nscene verse { lead = tone.melody }\nsong { verse*2 }\n"))
	score := filepath.Join(root, "main.cicada")
	sources, err := project.ReadSources(score, nil)
	if err != nil {
		t.Fatal(err)
	}
	pins, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	vendortest.Write(t, root, "cicada.sum", pins)
	before, ds, err := project.LoadScore(score, nil)
	if err != nil || len(ds) != 0 {
		t.Fatalf("load source: %v %+v", err, ds)
	}
	var original bytes.Buffer
	if _, err := render.WAV(before, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Bars: 2}, &original); err != nil {
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
	unpacked := t.TempDir()
	entries := map[string][]byte{}
	for _, file := range archive.File {
		if !filepath.IsLocal(file.Name) || strings.Contains(file.Name, "\\") {
			t.Fatalf("unsafe name: %s", file.Name)
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = data
		vendortest.Write(t, unpacked, file.Name, data)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(entries["cicada-bundle.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "cicada.bundle/1" || manifest.Entry != "main.cicada" || len(manifest.Files) != len(entries)-1 {
		t.Fatal(manifest)
	}
	if len(manifest.Libraries) != 1 || manifest.Libraries[0].Path != "demo/tone" || manifest.Libraries[0].Kind != "project" || len(manifest.Libraries[0].SHA256) != 64 {
		t.Fatalf("missing vendored library provenance: %+v", manifest.Libraries)
	}
	for _, file := range manifest.Files {
		data, ok := entries[file.Path]
		sum := sha256.Sum256(data)
		if !ok || file.Bytes != int64(len(data)) || file.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("invalid inventory: %+v", file)
		}
	}
	if bytes.Contains(entries["cicada-bundle.json"], []byte(root)) || bytes.Contains(entries["cicada-bundle.json"], []byte(user)) {
		t.Fatal("private source path in manifest")
	}
	if !bytes.Equal(entries["lib/demo/tone/audio/nested/tone.wav"], audio) {
		t.Fatal("library audio missing")
	}
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	after, ds, err := project.LoadScore(filepath.Join(unpacked, manifest.Entry), nil)
	if err != nil || len(ds) != 0 {
		t.Fatalf("load extracted bundle: %v %+v", err, ds)
	}
	var copied bytes.Buffer
	if _, err := render.WAV(after, render.Options{AssetRoot: unpacked, SampleRate: 48000, Bits: 32, Bars: 2}, &copied); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original.Bytes(), copied.Bytes()) {
		t.Fatal("extracted score renders differently")
	}
	t.Setenv("CICADA_LIBRARY", user)
	if err := bundleCommand([]string{score, path}); err == nil {
		t.Fatal("overwrote existing bundle")
	}
}

func TestBundleRejectsInvalidInputWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "bad.cicada")
	if err := os.WriteFile(score, []byte("not a score"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "song.zip")
	if err := bundleCommand([]string{score, target}); err == nil {
		t.Fatal("accepted invalid score")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected output: %v", err)
	}
}

func TestBundleMultiFileIsDeterministic(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "multifile", "main.cicada")
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.zip"), filepath.Join(dir, "second.zip")
	for _, target := range []string{first, second} {
		if err := bundleCommand([]string{score, target}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("unchanged project produced different archives")
	}
	archive, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	entries := map[string]bool{}
	for _, file := range archive.File {
		entries[file.Name] = true
	}
	for _, name := range []string{"main.cicada", "parts/patterns.cicada", "parts/voices.cicada", "cicada.mod", "cicada-bundle.json"} {
		if !entries[name] {
			t.Fatalf("missing %s", name)
		}
	}
}
