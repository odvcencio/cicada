package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
)

func TestProjectCheckReportsEachScoreWithSourceCaret(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "a.cicada")
	invalid := filepath.Join(root, "songs", "b.cicada")
	if err := os.Mkdir(filepath.Dir(invalid), 0755); err != nil {
		t.Fatal(err)
	}
	validSource := "title \"A\"\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene main { bass = pulse }\nsong { main }\n"
	invalidSource := "title \"B\"\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene main { bass = missing }\nsong { main }\n"
	if err := os.WriteFile(valid, []byte(validSource), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(invalidSource), 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := projectScorePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if err := checkPaths(paths, &output, &diagnostics); err == nil || !strings.Contains(err.Error(), "1 error(s)") {
		t.Fatalf("invalid project passed: %v", err)
	}
	if got := output.String(); !strings.Contains(got, valid) || strings.Contains(got, invalid) {
		t.Fatalf("valid score report: %s", got)
	}
	if got := diagnostics.String(); !strings.Contains(got, invalid+":4:") || !strings.Contains(got, "CICADA-REFERENCE") || !strings.Contains(got, "bass = missing") || !strings.Contains(got, "^") || !strings.Contains(got, "use a declared pattern name") {
		t.Fatalf("missing diagnostic context: %s", got)
	}
	if current, err := os.ReadFile(invalid); err != nil || string(current) != invalidSource {
		t.Fatalf("check changed source: %q, %v", current, err)
	}
}

func TestCheckCaretPreservesTabStops(t *testing.T) {
	var output bytes.Buffer
	printCheckDiagnostic(&output, "score.cicada", []byte("a\tbc\n"), notation.Diagnostic{
		Code: "CICADA-SYNTAX", Severity: "error", Message: "bad token",
		Position: notation.Position{Line: 1, Column: 4},
	})
	if !strings.Contains(output.String(), "  a\tbc\n   \t ^\n") {
		t.Fatalf("caret did not preserve tab stop: %q", output.String())
	}
}

func TestCheckAudioAssetsFromManifestRoot(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"audio", "scores"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project audio-test\ncicada 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data := testwav.Bytes(48000, 1, 16, 4800, 1)
	wav := filepath.Join(root, "audio", "example.wav")
	if err := os.WriteFile(wav, data, 0600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf("asset vocal \"audio/example.wav\" { sha256 = \"%x\" format = wav frames = 4800 rate = 48000Hz channels = 1 }\nclip region vocal {}\ntrack vox audio {}\nscene verse {vox=region}\nsong {verse}\n", sha256.Sum256(data))
	path := filepath.Join(root, "scores", "main.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if err := checkPaths([]string{path}, &out, &diagnostics); err != nil {
		t.Fatalf("valid audio score: %v %s", err, diagnostics.String())
	}
	if err := os.Remove(wav); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if err := checkPaths([]string{path}, &out, &diagnostics); err == nil {
		t.Fatal("missing asset passed check")
	}
	for _, want := range []string{path + ":1:1:", "CICADA-ASSET-MISSING", "expected", "actual"} {
		if !strings.Contains(diagnostics.String(), want) {
			t.Errorf("missing %q: %s", want, diagnostics.String())
		}
	}
}
