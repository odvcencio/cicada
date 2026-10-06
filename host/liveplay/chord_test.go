package liveplay

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"strings"
	"testing"
)

func TestPolyLiveNotesReturnExplicitUnsupportedError(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceGraph, Polyphony: 4, Graph: graph.Program{Len: 2, Output: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}}}}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "keys", Kind: "piano"}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, false} {
		err := p.Note("keys", 60, 100, on)
		if err == nil || !strings.Contains(err.Error(), "handle-aware") || !strings.Contains(err.Error(), "NoteOn/NoteOff") {
			t.Fatalf("unclear poly live error: %v", err)
		}
	}
}
