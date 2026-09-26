package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectFormatIsCheckedBeforeAnyScoreIsWritten(t *testing.T) {
	root := t.TempDir()
	score := filepath.Join(root, "a.cicada")
	original := []byte("title \"A\"\ntrack bass acid {}\npattern pulse {1 . 5 .}\nscene main {bass=pulse}\nsong {main}\n")
	if err := os.WriteFile(score, original, 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "z.cicada")
	if err := os.WriteFile(bad, []byte("pattern { !!!"), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if err := formatProject(root, false, &output, &diagnostics); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("invalid score did not stop project format: %v", err)
	}
	if current, err := os.ReadFile(score); err != nil || !bytes.Equal(current, original) {
		t.Fatalf("valid score changed before all scores parsed: %q, %v", current, err)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(root, "songs")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	subscore := filepath.Join(subdir, "second.cicada")
	if err := os.WriteFile(subscore, original, 0600); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(root, ".draft")
	if err := os.Mkdir(hidden, 0755); err != nil {
		t.Fatal(err)
	}
	hiddenScore := filepath.Join(hidden, "idea.cicada")
	if err := os.WriteFile(hiddenScore, original, 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "cicada.mod"), []byte("project nested\ncicada 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nestedScore := filepath.Join(nested, "own.cicada")
	if err := os.WriteFile(nestedScore, original, 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	diagnostics.Reset()
	if err := formatProject(root, true, &output, &diagnostics); err == nil || !strings.Contains(err.Error(), "3 score(s)") {
		t.Fatalf("check did not report format change: %v", err)
	}
	if current, err := os.ReadFile(score); err != nil || !bytes.Equal(current, original) {
		t.Fatalf("check wrote score: %q, %v", current, err)
	}
	if err := formatProject(root, false, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(score); err != nil || bytes.Equal(current, original) {
		t.Fatalf("project score was not formatted: %q, %v", current, err)
	}
	if current, err := os.ReadFile(subscore); err != nil || bytes.Equal(current, original) {
		t.Fatalf("score in project subdirectory was not formatted: %q, %v", current, err)
	}
	if current, err := os.ReadFile(hiddenScore); err != nil || bytes.Equal(current, original) {
		t.Fatalf("score in hidden project directory was not formatted: %q, %v", current, err)
	}
	if current, err := os.ReadFile(nestedScore); err != nil || !bytes.Equal(current, original) {
		t.Fatalf("nested project score changed: %q, %v", current, err)
	}
	if err := formatProject(root, true, &output, &diagnostics); err != nil {
		t.Fatalf("formatted project did not pass check: %v", err)
	}
}
