package liveplay

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"testing"
)

func TestPolyLiveNotesUseOwnedKernelCommands(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 4}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceGraph, Polyphony: 4, Graph: graph.Program{Len: 2, Output: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}}}}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "keys", Kind: "custom", Pitched: true}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, note := range []int{60, 64} {
		if err := p.Note("keys", note, 100, true); err != nil {
			t.Fatal(err)
		}
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if p.ownedFinalCount != 2 {
		t.Fatal("poly notes collapsed", p.ownedFinalCount)
	}
	for i := 0; i < p.ownedFinalCount; i++ {
		if p.ownedFinal[i].gate.Arg1 != cmd.LiveIdentity || p.ownedFinal[i].gate.Index == 0 {
			t.Fatal("missing kernel ownership", p.ownedFinal[i])
		}
	}
	for _, note := range []int{60, 64} {
		if err := p.Note("keys", note, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
}
