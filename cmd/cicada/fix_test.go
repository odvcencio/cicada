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
	for _, old := range [][]byte{[]byte("phrase hook acid"), []byte("pattern lead-a notes"), []byte("steps ="), []byte("use hook transpose =")} {
		if bytes.Contains(fixed, old) {
			t.Fatalf("legacy spelling remains: %s", old)
		}
	}
	if !bytes.Contains(fixed, []byte("use hook +12")) {
		t.Fatal("phrase transpose was not shortened")
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

func TestFixChanceAndTerminatorsPreserveComments(t *testing.T) {
	source := []byte("cicada 1\n// keep 2%70; literally\ninstrument bass { param amount = 50%; param cutoff = 720hz; voice mono { out = saw(pitch) * amount; } }\ntrack low bass { level = -6db }\npattern p notes steps=1 { c%70 }\nscene main { low=p }\nsong { main }\n")
	fixed, changed, err := fixSource(source)
	if err != nil || !changed {
		t.Fatalf("fix: %v", err)
	}
	if !bytes.Contains(fixed, []byte("c?70")) || bytes.Contains(fixed, []byte("c%70")) {
		t.Fatalf("chance not migrated: %s", fixed)
	}
	if !bytes.Contains(fixed, []byte("// keep 2%70; literally")) {
		t.Fatal("comment changed")
	}
	if !bytes.Contains(fixed, []byte("720Hz")) || !bytes.Contains(fixed, []byte("-6dB")) {
		t.Fatal("SI unit spelling was not applied")
	}
	if bytes.Contains(fixed, []byte("50%;")) || bytes.Contains(fixed, []byte("amount;")) {
		t.Fatal("terminator remains")
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

func TestFixPublishedExamplesPreservesSemantics(t *testing.T) {
	paths, err := filepath.Glob("../../examples/*.cicada")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("published examples missing")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fixed, _, err := fixSource(source)
			if err != nil {
				t.Fatal(err)
			}
			_, changed, err := fixSource(fixed)
			if err != nil || changed {
				t.Fatalf("fix is not stable: %v", err)
			}
		})
	}
}
