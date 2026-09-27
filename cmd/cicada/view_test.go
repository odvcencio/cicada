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

func TestScoreViewShowsNonDefaultMelodicVelocity(t *testing.T) {
	input := filepath.Join("..", "..", "examples", "first-acid.cicada")
	p, err := loadProject(input)
	if err != nil {
		t.Fatal(err)
	}
	var melodic *project.Pattern
	for index := range p.Patterns {
		if p.Patterns[index].Kind != "drums" && len(p.Patterns[index].Data) > 0 && p.Patterns[index].Data[0] != nil {
			melodic = &p.Patterns[index]
			break
		}
	}
	if melodic == nil {
		t.Fatal("example has no active melodic step")
	}
	melodic.Data[0].Velocity = 73
	var output bytes.Buffer
	if err := writeScoreView(&output, p, "velocity test", "score.cicada"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `aria-label="Velocity 73"`) || !strings.Contains(output.String(), `>73</span>`) {
		t.Fatal("non-default melodic velocity is not visible in the score view")
	}
}

func TestScoreViewMakesLegacyStopPatternLaunchable(t *testing.T) {
	source := "title \"Legacy stop\"\ntrack bass acid {}\npattern stop acid steps=4 { 1 . 5 . }\nscene main { bass=stop }\nsong { main }\n"
	path := filepath.Join(t.TempDir(), "legacy-stop.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := loadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeScorePage(&output, p, source, filepath.Base(path), true, studioRevision([]byte(source))); err != nil {
		t.Fatal(err)
	}
	page := output.String()
	if !strings.Contains(page, `<button type="button" class="scene-slot" data-track="bass" data-pattern="stop"`) {
		index := strings.Index(page, `aria-label="Scene launch matrix"`)
		matrix := "scene matrix not rendered"
		if index >= 0 {
			matrix = page[index:min(len(page), index+1000)]
		}
		t.Fatalf("a real pattern named stop has no launch button; scenes=%+v patterns=%+v matrix=%s", p.Scenes, p.Patterns, matrix)
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
