package liveplay

import (
	"encoding/binary"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func authoredNotePlayer(t *testing.T, poly bool) *Player {
	t.Helper()
	program := graph.Program{Len: 13, Output: 12}
	program.Nodes[0] = graph.Node{Op: graph.Pitch}
	program.Nodes[1] = graph.Node{Op: graph.Gate}
	program.Nodes[2] = graph.Node{Op: graph.Velocity}
	program.Nodes[3] = graph.Node{Op: graph.Constant, Value: 2}
	program.Nodes[4] = graph.Node{Op: graph.Constant, Value: 10}
	program.Nodes[5] = graph.Node{Op: graph.Constant, Value: 1}
	program.Nodes[6] = graph.Node{Op: graph.Constant, Value: 5}
	program.Nodes[7] = graph.Node{Op: graph.ADSR, A: 1, B: 3, C: 4, D: 5, E: 6}
	program.Nodes[8] = graph.Node{Op: graph.Sine, A: 0}
	program.Nodes[9] = graph.Node{Op: graph.Multiply, A: 7, B: 8}
	program.Nodes[10] = graph.Node{Op: graph.Multiply, A: 9, B: 2}
	program.Nodes[11] = graph.Node{Op: graph.Constant, Value: .1}
	program.Nodes[12] = graph.Node{Op: graph.Multiply, A: 10, B: 11}
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 8, BPMMilli: 120_000}
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	kind := "graph"
	if poly {
		cfg.Track[0].Kind, kind = engine.VoiceGraphPoly, "poly"
	}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "keys", Kind: kind}}}, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func authoredNotePower(t *testing.T, p *Player) float64 {
	t.Helper()
	// Wait past short releases and limiter lookahead, then measure a complete
	// frequency cycle range instead of one instantaneous output sample.
	buffer := make([]byte, blockFrames*8)
	for n := 0; n < 32; n++ {
		if _, err := p.Read(buffer); err != nil {
			t.Fatal(err)
		}
	}
	var power float64
	for n := 0; n < 16; n++ {
		if _, err := p.Read(buffer); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < blockFrames; i++ {
			value := math.Float32frombits(binary.LittleEndian.Uint32(buffer[i*8:]))
			power += float64(value) * float64(value)
		}
	}
	return power / (16 * blockFrames)
}

func TestLivePolyNoteOffKeepsOtherHeldPitches(t *testing.T) {
	p := authoredNotePlayer(t, true)
	for _, note := range []int{60, 64, 67} {
		if err := p.Note("keys", note, 100, true); err != nil {
			t.Fatal(err)
		}
	}
	if power := authoredNotePower(t, p); power < .0001 {
		t.Fatalf("authored chord is silent: %g", power)
	}
	if err := p.Note("keys", 60, 0, false); err != nil {
		t.Fatal(err)
	}
	if power := authoredNotePower(t, p); power < .0001 {
		t.Fatalf("one note-off released the whole chord: %g", power)
	}
	for _, note := range []int{64, 67} {
		if err := p.Note("keys", note, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if power := authoredNotePower(t, p); power > 1e-12 {
		t.Fatalf("all released pitches still sound: %g", power)
	}
}

func TestLiveMonoAuthoredNoteIgnoresEarlierPitchOff(t *testing.T) {
	p := authoredNotePlayer(t, false)
	for _, note := range []int{60, 64} {
		if err := p.Note("keys", note, 100, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Note("keys", 60, 0, false); err != nil {
		t.Fatal(err)
	}
	if power := authoredNotePower(t, p); power < .0001 {
		t.Fatalf("earlier mono key released the current key: %g", power)
	}
	if err := p.Note("keys", 64, 0, false); err != nil {
		t.Fatal(err)
	}
	if power := authoredNotePower(t, p); power > 1e-12 {
		t.Fatalf("current mono key did not release: %g", power)
	}
}
