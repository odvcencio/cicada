package main

import (
	"math"
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
// section, without the indentation of the list item that holds it. The fence
// shows how far the item is indented, which is more for step 10 than step 9.
func manualBlock(t *testing.T, language string) string {
	t.Helper()
	lines := manualSection(t)
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```"+language {
			continue
		}
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
		var block []string
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
			block = append(block, strings.TrimPrefix(lines[i], indent))
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

// The guide gives the frame budget of a piece in frames and in seconds. The
// seconds must be what the limit comes to, or a player plans a take that the
// command then refuses.
func TestRecordKitManualStatesTheFrameBudget(t *testing.T) {
	text := strings.Join(manualSection(t), "\n")
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"8,388,608 frames, which is " + strconv.Itoa(int(math.Round(float64(recording.MaxFrames)/48000))) + " seconds at 48 kHz or " + strconv.Itoa(int(math.Round(float64(recording.MaxFrames)/44100))) + " seconds at 44.1 kHz",
		"8,388,608 source frames (" + strconv.Itoa(int(math.Round(float64(recording.MaxFrames)/48000))) + " seconds at 48 kHz",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
	if recording.MaxFrames != 8388608 {
		t.Errorf("the limit is %d frames; the guide says 8,388,608", recording.MaxFrames)
	}
	// The worked example: a crash of 12 seconds, 5 hits in each of 3 dynamics.
	limit := float64(recording.MaxFrames) / 48000
	if float64(12*5*3) <= limit || float64(12*4*3) >= limit {
		t.Errorf("the guide's worked example (180 s, or 144 s with 4 hits) does not straddle the limit of %.1f s", limit)
	}
	for _, want := range []string{"takes 180 seconds, which is too long", "Four hits per dynamic take 144 seconds and fit"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
}
