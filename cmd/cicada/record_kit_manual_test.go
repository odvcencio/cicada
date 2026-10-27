package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/internal/testwav"
)

// manualSection returns the lines of the drum kit section of the manual.
func manualSection(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs", "manual", "recording-instruments.md"))
	if err != nil {
		t.Fatal(err)
	}
	var section []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = line == "## Record your own drum kit"
			continue
		}
		if in {
			section = append(section, line)
		}
	}
	if len(section) == 0 {
		t.Fatal("the manual has no Record your own drum kit section")
	}
	return section
}

// manualBlock returns the first fenced block of the given language in the
// section, without the indentation of the list item that holds it.
func manualBlock(t *testing.T, language string) string {
	t.Helper()
	lines := manualSection(t)
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```"+language {
			continue
		}
		var block []string
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
			block = append(block, strings.TrimPrefix(lines[i], "   "))
		}
		return strings.Join(block, "\n") + "\n"
	}
	t.Fatalf("the section has no %s block", language)
	return ""
}

// The manual's recipe, note table and starter score are what the command
// reads, names and writes.
func TestRecordKitManualMatchesTheCommand(t *testing.T) {
	for _, line := range manualSection(t) {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 3 {
			continue
		}
		note, err := strconv.Atoi(strings.TrimSpace(cells[1]))
		if err != nil {
			continue
		}
		if got := strings.Trim(strings.TrimSpace(cells[2]), "`"); got != recording.NoteName(note) {
			t.Errorf("the manual spells note %d as %s, the score spells it %s", note, got, recording.NoteName(note))
		}
	}
	recipe, err := recording.ParseKitRecipe([]byte(manualBlock(t, "json")))
	if err != nil {
		t.Fatal("the manual's recipe is refused:", err)
	}
	drums := map[string]testwav.Drum{"kick": testwav.Kick, "snare": testwav.Snare, "closed_hat": testwav.ClosedHat, "pedal_hat": testwav.ClosedHat, "open_hat": testwav.OpenHat, "crash": testwav.Crash}
	takes := fstest.MapFS{}
	var names []string
	for _, piece := range recipe.Pieces {
		names = append(names, piece.Name)
		for i, take := range piece.Takes {
			level := []float64{.1, .3, .8}[i%3]
			takes[take] = &fstest.MapFile{Data: testwav.Take(48000, drums[piece.Name], level, level*1.05)}
		}
	}
	if strings.Join(names, " ") != "kick snare closed_hat pedal_hat open_hat crash" {
		t.Fatalf("the manual's recipe lists %v", names)
	}
	pack, _, err := recording.BuildKit("studio", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	// The pin and the path differ for every kit; the score around them is
	// the starter the manual shows.
	score := func(source string) string {
		var kept []string
		for _, line := range strings.Split(source, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "pack =") && !strings.HasPrefix(strings.TrimSpace(line), "sha256 =") {
				kept = append(kept, line)
			}
		}
		return strings.Join(kept, "\n")
	}
	if got, want := score(string(pack.Files["instrument.cicada"])), score(manualBlock(t, "cicada")); got != want {
		t.Fatalf("the starter score differs from the manual's:\n%s\nmanual:\n%s", got, want)
	}
}
