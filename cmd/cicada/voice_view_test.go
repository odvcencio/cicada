package main

import (
	"bytes"
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
