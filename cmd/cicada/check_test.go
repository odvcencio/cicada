package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
