package liveplay

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func TestPolyLiveExpressionReturnsExplicitUnsupportedError(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 4, BPMMilli: 120000}
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
	if err := p.NoteExpression("keys", 7, 25, .5, .75); err == nil || !strings.Contains(err.Error(), "handle-aware") {
		t.Fatalf("unclear poly expression error: %v", err)
	}
}

func TestLiveExpressionQueuesIdentityAndFullResolution(t *testing.T) {
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.NoteWithID("bass", 60, 100, true, 42); err != nil {
		t.Fatal(err)
	}
	if err := p.NoteExpression("bass", 42, 25.125, .500001, .75); err != nil {
		t.Fatal(err)
	}
	if err := p.NoteWithID("bass", 60, 0, false, 42); err != nil {
		t.Fatal(err)
	}
	batch := p.notes.Load()
	if batch.count != 3 || batch.inputs[0].NoteID != 42 || !batch.inputs[1].Expression || batch.inputs[1].Pressure != float32(.500001) || batch.inputs[2].NoteID != 42 {
		t.Fatalf("live input lost ordered identity or expression: %+v", batch)
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if p.fault != nil {
		t.Fatal(p.fault)
	}
}

func TestLiveExpressionValidationAndAllocationFreeDrain(t *testing.T) {
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, invalid := range []struct {
		track                   string
		id                      uint16
		cents, pressure, timbre float32
	}{
		{"bass", 0, 0, 0, .5}, {"bass", 65535, 0, 0, .5},
		{"missing", 1, 0, 0, .5}, {"drums", 1, 0, 0, .5},
		{"bass", 1, 9601, 0, .5}, {"bass", 1, -9601, 0, .5},
		{"bass", 1, 0, -1, .5}, {"bass", 1, 0, 1.1, .5},
		{"bass", 1, 0, 0, -1}, {"bass", 1, 0, 0, 1.1},
		{"bass", 1, float32(math.NaN()), 0, .5}, {"bass", 1, 0, float32(math.Inf(1)), .5},
	} {
		if err := p.NoteExpression(invalid.track, invalid.id, invalid.cents, invalid.pressure, invalid.timbre); err == nil {
			t.Fatalf("accepted invalid expression %+v", invalid)
		}
	}
	batch := &noteBatch{count: 1}
	batch.inputs[0] = noteInput{Track: "bass", NoteID: 1, Expression: true, PitchCents: 23.125, Pressure: .5, Timbre: .75}
	var left, right [blockFrames]float32
	allocs := testing.AllocsPerRun(100, func() {
		p.notes.Store(batch)
		p.queueLiveNotes()
		p.current.Engine.Render(left[:], right[:])
	})
	if allocs != 0 {
		t.Fatalf("expression drain/render allocated %g objects", allocs)
	}
}

func TestLiveExpressionReachesCustomGraphInstrument(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceGraph
	cfg.Track[0].Graph = graph.Program{Len: 1, Output: 0, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pressure}}}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "lead", Kind: "custom", Pitched: true}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.NoteWithID("lead", 60, 100, true, 7); err != nil {
		t.Fatal(err)
	}
	if err := p.NoteExpression("lead", 7, 0, .5, .75); err != nil {
		t.Fatal(err)
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	var sounded bool
	for _, sample := range pcm {
		sounded = sounded || sample != 0
	}
	if !sounded {
		t.Fatal("custom graph did not receive live pressure")
	}
}
