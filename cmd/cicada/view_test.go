package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestScoreViewProjectsValidatedMusicAndEscapesSource(t *testing.T) {
	input := filepath.Join("..", "..", "examples", "first-acid.cicada")
	p, err := loadProject(input)
	if err != nil {
		t.Fatal(err)
	}
	p.Title = `<script>alert("score")</script>`
	source := "// <script>alert('source')</script>\n" + string(mustReadViewTest(t, input))
	var output bytes.Buffer
	if err := writeScoreView(&output, p, source, filepath.Base(input)); err != nil {
		t.Fatal(err)
	}
	page := output.String()
	for _, expected := range []string{"bass-a", "lead-a", "bd", "Step 1, note A2", "source of truth", "validated snapshot", "&lt;script&gt;"} {
		if !strings.Contains(page, expected) {
			t.Errorf("score view lacks %q", expected)
		}
	}
	if strings.Contains(page, "<script>") {
		t.Fatal("score source or title escaped the HTML boundary")
	}
}

func TestScoreViewAcceptsSemanticJSON(t *testing.T) {
	input := filepath.Join("..", "..", "examples", "first-acid.cicada")
	p, err := loadProject(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := project.CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "score.json")
	outPath := filepath.Join(dir, "view.html")
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := viewCommand([]string{jsonPath, "-o", outPath}); err != nil {
		t.Fatal(err)
	}
	page := string(mustReadViewTest(t, outPath))
	if !strings.Contains(page, "bass-a") || !strings.Contains(page, "First acid") {
		t.Fatal("JSON score view lost pattern or canonical source")
	}
}

func mustReadViewTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
