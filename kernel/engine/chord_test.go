package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func polyConfig() Config {
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120000, Patterns: []PatternBank{{}}}
	cfg.Track[0] = TrackConfig{Kind: VoiceGraph, Polyphony: 4, Graph: graph.Program{Len: 6, Output: 5, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}, {Op: graph.Gate}, {Op: graph.Constant, Value: 100}, {Op: graph.Envelope, A: 2, B: 3}, {Op: graph.Multiply, A: 1, B: 4}}}}
	p := seq.Pattern{Len: 4, GatePercent: 55}
	p.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	p.Chords[0] = seq.ChordStep{Notes: [4]uint8{60, 64, 67}, Count: 3}
	for i := 1; i < 4; i++ {
		p.Steps[i], _ = seq.PackStep(seq.Step{Ratchet: 1, Probability: 100})
	}
	cfg.Patterns[0].Slots[0] = p
	return cfg
}

func TestPatternPreloadCopiesCallerStorage(t *testing.T) {
	for _, poly := range []bool{false, true} {
		cfg := polyConfig()
		if !poly {
			cfg.Track[0].Polyphony = 0
			cfg.Patterns[0].Slots[0].Chords = [64]seq.ChordStep{}
		}
		want := cfg.Patterns[0].Slots[0]
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Patterns[0].Slots[0] = seq.Pattern{}
		if e.patterns[0].slots[0] != want {
			t.Fatal("preloaded pattern aliases caller storage")
		}
		e.Push(cmd.Command{Op: cmd.OpSetPatternLen, Track: 0, Index: 1})
		var left, right [128]float32
		e.Render(left[:], right[:])
		if cfg.Patterns[0].Slots[0] != (seq.Pattern{}) {
			t.Fatal("engine edit mutated caller storage")
		}
	}
}
func TestPolyKernelRejectsLegacyLiveCommandsAndBadBudgets(t *testing.T) {
	cfg := polyConfig()
	for _, op := range []cmd.Op{cmd.OpNoteOn, cmd.OpNoteOff} {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Push(cmd.Command{Op: op, Track: 0, Arg0: 60 | 100<<8}) {
			t.Fatal("wire command unexpectedly rejected before typed fault")
		}
		var l, r [128]float32
		e.Render(l[:], r[:])
		var m cmd.Message
		fault := false
		for e.Poll(&m) {
			if m.Kind == cmd.Fault && m.A == cmd.FaultPolyLive {
				fault = true
			}
		}
		if !fault {
			t.Fatal("legacy live poly command silently accepted")
		}
	}
	for _, mode := range []uint8{1, 2, 3, 5, 255} {
		bad := cfg
		bad.Track[0].Polyphony = mode
		if _, err := New(bad); err == nil {
			t.Fatalf("invalid polyphony%d", mode)
		}
	}
	bad := cfg
	bad.MaxVoices = 3
	if _, err := New(bad); err == nil {
		t.Fatal("four voices exceeded declared budget")
	}
	bad = cfg
	bad.Track[0].Polyphony = 0
	if _, err := New(bad); err == nil {
		t.Fatal("mono accepted a chord payload")
	}
}
func TestPolyKernelPatternPlaybackAllocationFree(t *testing.T) {
	e, err := New(polyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0}) || !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("play rejected")
	}
	var l, r [128]float32
	e.Render(l[:], r[:])
	if e.voices[0].poly.ActiveVoices() != 3 {
		t.Fatal("actual pattern did not start all three voices")
	}
	alloc := testing.AllocsPerRun(1000, func() {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
		}
	})
	if alloc != 0 {
		t.Fatalf("poly kernel callback allocated%g", alloc)
	}
	e.Reset()
	if e.voices[0].poly.ActiveVoices() != 0 {
		t.Fatal("reset retained voices")
	}
}

func TestPolyPatternSwitchCannotLeaveOldChordGated(t *testing.T) {
	cfg := polyConfig()
	p := &cfg.Patterns[0].Slots[0]
	p.Chords[0] = seq.ChordStep{Notes: [4]uint8{60, 64, 67, 72}, Count: 4}
	for i := 1; i < 4; i++ {
		p.Steps[i], _ = seq.PackStep(seq.Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100})
	}
	target := seq.Pattern{Len: 4, GatePercent: 55}
	target.Steps[0], _ = seq.PackStep(seq.Step{Note: 62, Gate: true, Ratchet: 1, Probability: 100})
	target.Chords[0] = seq.ChordStep{Notes: [4]uint8{62, 65}, Count: 2}
	for i := 1; i < 4; i++ {
		target.Steps[i], _ = seq.PackStep(seq.Step{Ratchet: 1, Probability: 100})
	}
	cfg.Patterns[0].Slots[1] = target
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []cmd.Command{{Op: cmd.OpSelectPattern, Track: 0, Index: 0}, {Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg1: 1, Tick: 240}} {
		if !e.Push(c) {
			t.Fatal("command rejected")
		}
	}
	var l, r [128]float32
	for at := 0; at < 15000; at += 128 {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault %+v", m)
			}
		}
	}
	if n := e.voices[0].poly.ActiveVoices(); n != 0 {
		t.Fatalf("shorter replacement chord left %d old slots gated", n)
	}
}

func TestPolyCohortEmitsOneReleaseMessage(t *testing.T) {
	e, err := New(polyConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var l, r [128]float32
	offs := 0
	for at := 0; at < 16000; at += 128 {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.NoteOff {
				offs++
			}
		}
	}
	if offs != 1 {
		t.Fatalf("one chord emitted %d release messages", offs)
	}
}
func TestPolySinglePitchSlideWaitsForPendingSwitch(t *testing.T) {
	cfg := polyConfig()
	cfg.Patterns[0].Slots[0].Chords = [64]seq.ChordStep{}
	cfg.Patterns[0].Slots[0].Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Slide: true, Ratchet: 1, Probability: 100, Velocity: 100})
	target := seq.Pattern{Len: 1, GatePercent: 55}
	target.Steps[0], _ = seq.PackStep(seq.Step{Note: 62, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	cfg.Patterns[0].Slots[1] = target
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []cmd.Command{{Op: cmd.OpSelectPattern, Track: 0, Index: 0}, {Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg1: 1, Tick: 240}} {
		e.Push(c)
	}
	var l, r [128]float32
	for at := 0; at < 5888; at += 128 {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault %+v", m)
			}
		}
	}
	if n := e.voices[0].poly.ActiveVoices(); n != 1 {
		t.Fatalf("pending slide was released before its target (%d active)", n)
	}
}

func TestPolyGenerationExhaustionFailsBeforeReuse(t *testing.T) {
	e, err := New(polyConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.patterns[0].generation = ^uint32(0)
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	var l, r [128]float32
	e.Render(l[:], r[:])
	var m cmd.Message
	found := false
	for e.Poll(&m) {
		if m.Kind == cmd.Fault && m.A == 17 {
			found = true
		}
	}
	if !found || e.patterns[0].generation != ^uint32(0) {
		t.Fatal("generation wrapped into an old cohort")
	}
}
