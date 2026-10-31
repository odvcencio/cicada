package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The documented Classic Drum Study passes cicada check with the neutral
// names, and the same score with the older names passes too.
func TestDrumMachinesExampleChecksWithNewAndOldNames(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	dir := filepath.Join(repositoryRoot(), "examples", "drum-machines")
	source, err := os.ReadFile(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(dir, "cicada.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "drum-machines.boom-kit") || !strings.Contains(string(source), "drum-machines.punch-kit") {
		t.Fatal("example does not use the neutral kit names")
	}
	old := strings.NewReplacer("boom-kit", "eight-oh-eight", "punch-kit", "nine-oh-nine").Replace(string(source))
	oldDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(oldDir, "main.cicada"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "cicada.sum"), sum, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "main.cicada"), filepath.Join(oldDir, "main.cicada")} {
		var out, errOut bytes.Buffer
		if err := checkPaths([]string{path}, &out, &errOut); err != nil {
			t.Fatalf("%s: %v\n%s%s", path, err, out.String(), errOut.String())
		}
	}
}
