package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceViewShowsAuthoredSignalAndEffectiveTrackParameters(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "circuit-kit.cicada")
	p, err := loadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	voices := scoreVoices(p)
	var subline *viewVoice
	for i := range voices {
		if voices[i].ID == "subline" {
			subline = &voices[i]
			break
		}
	}
	if subline == nil || subline.Output.Label != "*" || len(subline.Lets) != 2 {
		t.Fatalf("authored signal graph missing: %+v", subline)
	}
	if len(subline.Params) != 1 || subline.Params[0].Default != "540Hz" || len(subline.Params[0].Overrides) != 1 || subline.Params[0].Overrides[0] != "bass: 620Hz" {
		t.Fatalf("parameter provenance missing: %+v", subline.Params)
	}
	var output bytes.Buffer
	if err := writeScoreView(&output, p, string(mustReadViewTest(t, path)), filepath.Base(path)); err != nil {
		t.Fatal(err)
	}
	page := output.String()
	for _, expected := range []string{`id="voice"`, "Signal bindings", "Output graph", "subline", "bass: 620Hz", `class="signal-chip operator"`} {
		if !strings.Contains(page, expected) {
			t.Errorf("voice projection lacks %q", expected)
		}
	}
}

func TestVoiceViewShowsKitLaneRoutingAndUsers(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "authored-kit.cicada")
	p, err := loadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	voices := scoreVoices(p)
	var kit *viewVoice
	var kick *viewVoice
	for i := range voices {
		switch voices[i].ID {
		case "steel":
			kit = &voices[i]
		case "kick":
			kick = &voices[i]
		}
	}
	if kit == nil || len(kit.Mappings) != 2 || len(kit.UsedBy) != 1 || kit.UsedBy[0] != "track drums" {
		t.Fatalf("kit routing or use missing: %+v", kit)
	}
	if kick == nil || len(kick.UsedBy) != 1 || kick.UsedBy[0] != "kit steel / BD" {
		t.Fatalf("kit instrument use missing: %+v", kick)
	}
}

// The starter's bass and chords are built-in keyboard patches. The view lists the
// built-in voices a score uses, so they appear with the tracks that play them.
func TestVoiceViewListsPianoAndKeyboardPatchesUsedByTracks(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("song", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	p, err := loadProject(filepath.Join(root, "song", "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]viewVoice{}
	for _, voice := range scoreVoices(p) {
		listed[voice.ID] = voice
	}
	for _, want := range []struct{ id, track string }{{"fm_bass", "track bass"}, {"poly_keys", "track chords"}, {"drums", "track drums"}} {
		voice, ok := listed[want.id]
		if !ok || len(voice.UsedBy) != 1 || voice.UsedBy[0] != want.track {
			t.Errorf("voice %s: %+v", want.id, voice)
		}
	}
	if voice := listed["fm_bass"]; voice.Kind != "built-in" || voice.Mode != "poly" || voice.Description != "Modeled keyboard" {
		t.Errorf("fm_bass is described as %+v", voice)
	}
	// A piano track is listed too, and voices no track uses are not.
	source := "cicada 2\ntrack lead piano {}\npattern line { c3 . g3 . }\nscene main { lead = line }\nsong { main }\n"
	path := filepath.Join(root, "piano.cicada")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	pianoProject, err := loadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	voices := scoreVoices(pianoProject)
	if len(voices) != 1 || voices[0].ID != "piano" || voices[0].Description != "Modeled grand piano" || voices[0].UsedBy[0] != "track lead" {
		t.Fatalf("piano score voices: %+v", voices)
	}
}
