package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/render"
)

func inTempWorkingDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	return dir
}

func TestNewProjectIsFormattedAndChecksWithStarterTracksAndScenes(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("fresh", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	projectDir := filepath.Join(root, "fresh")
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	if err := formatProjectCommand(true, &stdout, &stderr); err != nil {
		t.Fatalf("cicada fmt --check: %v\n%s", err, stderr.String())
	}
	mainPath := filepath.Join(root, "fresh", "main.cicada")
	if err := checkCommand(nil, &stdout, &stderr); err != nil {
		t.Fatalf("cicada check: %v\n%s", err, stderr.String())
	}
	inspection, err := inspectScore(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if hasDiagnosticErrors(inspection.diagnostics) {
		t.Fatalf("starter diagnostics: %+v", inspection.diagnostics)
	}
	if len(inspection.score.Tracks) != 2 || len(inspection.score.Scenes) != 2 {
		t.Fatalf("starter has %d tracks and %d scenes", len(inspection.score.Tracks), len(inspection.score.Scenes))
	}
	trackIDs := map[string]bool{}
	for _, track := range inspection.score.Tracks {
		trackIDs[track.Name] = true
	}
	if !trackIDs["bass"] || !trackIDs["drums"] {
		t.Fatalf("starter tracks: %v", trackIDs)
	}
	if bytes.Contains(inspection.source, []byte("steps = 16")) {
		t.Fatal("starter has a redundant steps = 16 header")
	}
	ignore, err := os.ReadFile(filepath.Join(root, "fresh", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{".cicada-studio-*", "*.revision", ".cicada/", "cicada.local"} {
		if !strings.Contains(string(ignore), pattern) {
			t.Errorf(".gitignore is missing %q", pattern)
		}
	}
}

func TestManualMixingExampleWorksInFreshProject(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("fresh", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(repositoryRoot(), "docs", "manual", "next-level.md")
	examples, err := readDocumentationExamples(docPath)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	for _, example := range examples {
		if example.kind == "cicada" {
			source = example.source
			break
		}
	}
	if source == "" {
		t.Fatal("next-level manual has no runnable Cicada mixing example")
	}
	mainPath := filepath.Join(root, "fresh", "main.cicada")
	if err := os.WriteFile(mainPath, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := fixCommand([]string{mainPath}); err != nil {
		t.Fatalf("cicada fix: %v", err)
	}
	if err := checkPaths([]string{mainPath}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("cicada check after fix: %v", err)
	}
	inspection, err := inspectScore(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	var wav bytes.Buffer
	if _, err := render.WAV(inspection.score, render.Options{SampleRate: 48_000, Bits: 32, Bars: 1, TailSec: 0, Block: 4096}, &wav); err != nil {
		t.Fatalf("cicada render after fix and check: %v", err)
	}
	if wav.Len() < 44 {
		t.Fatalf("render returned only %d WAV bytes", wav.Len())
	}
}
