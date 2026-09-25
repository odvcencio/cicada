package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixPreservesScoreAndIsIdempotent(t *testing.T) {
	source, err := os.ReadFile("../../examples/first-acid.cicada")
	if err != nil {
		t.Fatal(err)
	}
	fixed, changed, err := fixSource(source)
	if err != nil || !changed {
		t.Fatalf("first fix: changed %t, %v", changed, err)
	}
	if bytes.HasPrefix(fixed, []byte("cicada 1")) {
		t.Fatal("legacy header remains")
	}
	if !bytes.HasPrefix(fixed, []byte("title ")) {
		t.Fatal("legacy header left a leading blank line")
	}
	if !bytes.Contains(fixed, []byte("octave = 2")) {
		t.Fatal("instrument home octave is missing")
	}
	again, changed, err := fixSource(fixed)
	if err != nil || changed || !bytes.Equal(fixed, again) {
		t.Fatalf("second fix changed the score: %v", err)
	}
}

func TestFixCommandCreatesManifestAndCheckDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "night-circuit.cicada")
	source := []byte("cicada 1\n// keep my comment\ninstrument bass { voice mono { out = saw(pitch) } }\ntrack low bass {}\npattern p notes steps=1 { c }\nscene main { low=p }\nsong { main }\n")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixCommand([]string{path, "--check"}); err == nil || !strings.Contains(err.Error(), "fix needed") {
		t.Fatalf("check: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cicada.mod")); !os.IsNotExist(err) {
		t.Fatal("check wrote a manifest")
	}
	if err := fixCommand([]string{path}); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "cicada.mod"))
	if err != nil || string(manifest) != "project night-circuit\ncicada 1\n" {
		t.Fatalf("manifest: %q, %v", manifest, err)
	}
	fixed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fixed, []byte("// keep my comment")) || !bytes.Contains(fixed, []byte("octave = 2")) {
		t.Fatalf("source was not preserved: %s", fixed)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("score mode: %v, %v", info, err)
	}
	if err := fixCommand([]string{path, "--check"}); err != nil {
		t.Fatalf("fixed score needs another change: %v", err)
	}
}

func TestFixKeepsCRLF(t *testing.T) {
	source := []byte("cicada 1\r\n\r\n// preserved\r\ninstrument bass {\r\n  voice mono { out = saw(pitch) }\r\n}\r\ntrack low bass {}\r\npattern p notes steps=1 { c }\r\nscene main { low=p }\r\nsong { main }\r\n")
	fixed, changed, err := fixSource(source)
	if err != nil || !changed {
		t.Fatalf("fix CRLF: %v", err)
	}
	if !bytes.HasPrefix(fixed, []byte("// preserved\r\n")) || !bytes.Contains(fixed, []byte("{\r\n  octave = 2\r\n")) {
		t.Fatalf("line endings changed: %q", fixed)
	}
}

func TestFixRollsBackManifestWhenScoreWriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failed.cicada")
	source := []byte("cicada 1\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n")
	if err := os.WriteFile(path, source, 0644); err != nil {
		t.Fatal(err)
	}
	writeFailure := errors.New("injected score write failure")
	err := fixCommandWithWriter([]string{path}, func(string, []byte, os.FileMode) error { return writeFailure })
	if !errors.Is(err, writeFailure) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cicada.mod")); !os.IsNotExist(err) {
		t.Fatal("manifest left behind after failed migration")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, source) {
		t.Fatalf("score changed after failed migration: %v", err)
	}
}
