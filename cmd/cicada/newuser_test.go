package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
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
	if len(inspection.score.Tracks) != 3 || len(inspection.score.Scenes) != 2 {
		t.Fatalf("starter has %d tracks and %d scenes", len(inspection.score.Tracks), len(inspection.score.Scenes))
	}
	trackKinds := map[string]string{}
	for _, track := range inspection.score.Tracks {
		trackKinds[track.Name] = track.Kind
	}
	if trackKinds["drums"] != "drums" || trackKinds["bass"] != "fm_bass" || trackKinds["chords"] != "poly_keys" {
		t.Fatalf("starter tracks: %v", trackKinds)
	}
	if bytes.Contains(inspection.source, []byte("steps = 16")) {
		t.Fatal("starter has a redundant steps = 16 header")
	}
	if bytes.Contains(bytes.ToLower(inspection.source), []byte("acid")) {
		t.Fatalf("starter names the acid voice:\n%s", inspection.source)
	}
	// The two scenes must differ, or the song has no break.
	sceneBindings := func(index int) map[string]string {
		bound := map[string]string{}
		for _, binding := range inspection.score.Scenes[index].Bindings {
			bound[binding.Track] = binding.Pattern
		}
		return bound
	}
	if reflect.DeepEqual(sceneBindings(0), sceneBindings(1)) {
		t.Fatal("starter scenes main and break bind the same patterns")
	}
	// A new project must need no migration: fix --check reports "already fixed".
	if err := fixCommand([]string{mainPath, "--check"}); err != nil {
		t.Fatalf("cicada fix --check on a new project: %v", err)
	}
	// The three starter tracks must fit the portable browser profile.
	var portable bytes.Buffer
	if err := checkCommand([]string{"--profile", "browser"}, &portable, &stderr); err != nil {
		t.Fatalf("cicada check --profile browser: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(portable.String(), "3 of 16 tracks") {
		t.Fatalf("browser profile report: %s", portable.String())
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
