package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestStudioPitchGridWritesChosenNoteToSource(t *testing.T) {
	handler, path := studioTestHandler(t)
	page := studioCall(t, handler, "/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-pitch="48"`) || !strings.Contains(page.Body.String(), `data-pattern="pulse" data-step="2" data-pitch="52" aria-label="Clear pulse step 3 at E3"`) {
		t.Fatalf("studio did not project editable pitch rows with correct step indices: %d", page.Code)
	}
	pitch := 48 // C3, inside the displayed range of the fixture
	result := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision([]byte(studioScore)), Pattern: "pulse", Step: 1, Pitch: &pitch})
	if result.Code != 200 {
		t.Fatalf("pitch edit failed: %d %s", result.Code, result.Body.String())
	}
	source, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(source, []byte("{ 1 c3 5 . }")) {
		t.Fatalf("pitch grid did not write C3 into source: %s, %v", source, err)
	}
	result = studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(source), Pattern: "pulse", Step: 1, Pitch: &pitch})
	if result.Code != 200 {
		t.Fatalf("pitch clear failed: %d %s", result.Code, result.Body.String())
	}
	source, err = os.ReadFile(path)
	if err != nil || string(source) != studioScore {
		t.Fatalf("clicking the active pitch did not restore the rest: %s, %v", source, err)
	}
	activePitch := 52 // E3 at step 3
	result = studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(source), Pattern: "pulse", Step: 2, Pitch: &activePitch})
	if result.Code != 200 {
		t.Fatalf("active pitch clear failed: %d %s", result.Code, result.Body.String())
	}
	source, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(source, []byte("{ 1 . . . }")) {
		t.Fatalf("active pitch at step 3 cleared a different step: %s, %v", source, err)
	}
}

func TestPitchGridKeepsStepModifiersAndEditsSharedPhrases(t *testing.T) {
	source := []byte(strings.Replace(studioScore, "1 . 5 .", "1^?70 . 5 .", 1))
	updated, err := pitchedSource(source, "pulse", "", 0, 48)
	if err != nil || !bytes.Contains(updated, []byte("c3^?70 . 5 .")) {
		t.Fatalf("pitch edit lost modifiers: %s, %v", updated, err)
	}
	if _, err := pitchedSource(source, "pulse", "bd", 0, 48); err == nil {
		t.Fatal("pitch edit accepted a drum lane")
	}
	if _, err := pitchedSource(source, "pulse", "", 0, 128); err == nil {
		t.Fatal("pitch edit accepted a pitch outside MIDI range")
	}
	phrase := []byte("track bass acid {}\nphrase hook { 1 . }\npattern pulse { use hook*2 }\nscene main { bass = pulse }\nsong { main }\n")
	shared, err := pitchedSource(phrase, "pulse", "", 2, 48)
	if err != nil || !bytes.Contains(shared, []byte("phrase hook { c3 . }")) || !bytes.Contains(shared, []byte("use hook*2")) {
		t.Fatalf("expanded phrase pitch did not edit shared source: %s, %v", shared, err)
	}
	transposed := []byte("track bass acid {}\nphrase hook { 1 . }\npattern pulse { use hook use hook +7 }\nscene main { bass = pulse }\nsong { main }\n")
	shared, err = pitchedSource(transposed, "pulse", "", 2, 53) // F3 through a +7 phrase use
	if err != nil || !bytes.Contains(shared, []byte("phrase hook { a#2 . }")) {
		t.Fatalf("transposed phrase pitch did not invert its transpose: %s, %v", shared, err)
	}
}

func TestSourcePitchSpellsMIDIExtremes(t *testing.T) {
	for pitch, want := range map[int]string{0: "c0,", 48: "c3", 61: "c#4", 127: "g6'''"} {
		if got := sourcePitch(pitch); got != want {
			t.Errorf("MIDI %d: got %q, want %q", pitch, got, want)
		}
	}
}
