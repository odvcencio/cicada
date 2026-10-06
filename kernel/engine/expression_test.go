package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func expressionConfig() Config {
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind = VoiceGraph
	cfg.Track[0].Graph = graph.Program{Len: 1}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.Pressure}
	return cfg
}

func expressionCommand(identity uint16, pressure float32) cmd.Command {
	return cmd.Command{Op: cmd.OpNoteExpression, Track: 0, Index: identity, Arg1: math.Float32bits(pressure), Pad: math.Float32bits(.5)}
}

func TestPolyLiveExpressionPreservesUnsupportedGuard(t *testing.T) {
	e, err := New(polyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(expressionCommand(1, .5)) {
		t.Fatal("valid expression command rejected before the typed live guard")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	var message cmd.Message
	found := false
	for e.Poll(&message) {
		found = found || message.Kind == cmd.Fault && message.A == cmd.FaultPolyLive
	}
	if !found {
		t.Fatal("poly live expression did not report unsupported capability")
	}
}

func TestLiveExpressionTargetsIdentityAndSameTickNoteOn(t *testing.T) {
	e, err := New(expressionConfig())
	if err != nil {
		t.Fatal(err)
	}
	// The controls arrive before NoteOn; same-tick ordering applies them after it.
	commands := []cmd.Command{expressionCommand(7, .75), {Op: cmd.OpNoteOn, Track: 0, Index: 7, Arg0: 69 | 127<<8}}
	if !e.PushBatch(commands) {
		t.Fatal("batch rejected")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	if e.voices[0].graph.Next() != .75 {
		t.Fatal("same-tick expression was lost")
	}
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 8, Arg0: 69 | 127<<8}) || !e.Push(expressionCommand(7, 1)) || !e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 7}) {
		t.Fatal("identity commands rejected")
	}
	e.Render(left[:], right[:])
	if !e.voices[0].noteActive || e.voices[0].graph.Next() != 0 {
		t.Fatal("stale identity changed a new note")
	}
	e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 7})
	if !e.voices[0].noteActive {
		t.Fatal("old note release stopped the current note")
	}
	for _, identity := range []uint16{8, 0, 0xffff} {
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 8, Arg0: 69 | 127<<8})
		e.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: identity})
		if e.voices[0].noteActive {
			t.Fatalf("note off identity %d did not release", identity)
		}
	}
}

func TestTiedPatternExpressionIsBlockInvariantAndAllocationFree(t *testing.T) {
	cfg := expressionConfig()
	cfg.Track[0].Graph.Nodes[0].Op = graph.PitchBend
	p := seq.Pattern{Len: 4, GatePercent: 50, Expression: new([64]seq.Expression)}
	p.Steps[0], _ = seq.PackStep(seq.Step{Note: 69, Gate: true, Ratchet: 1, Probability: 100})
	p.Steps[1], _ = seq.PackStep(seq.Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100})
	p.Steps[2], _ = seq.PackStep(seq.Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100})
	p.Expression[0] = seq.Expression{Set: true, Timbre: .5}
	p.Expression[1] = seq.Expression{Set: true, PitchCents: 200, Timbre: .5}
	p.Expression[2] = seq.Expression{Set: true, Timbre: .5}
	cfg.Patterns = []PatternBank{{Slots: [16]seq.Pattern{p}}}
	for _, block := range []int{1, 64, 128} {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		var left, right [128]float32
		for frame := 0; frame < 12001; {
			n := min(block, 12001-frame)
			// Stop immediately after each boundary to inspect its resolved state.
			if frame < 6001 {
				n = min(n, 6001-frame)
			}
			e.Render(left[:n], right[:n])
			frame += n
			if frame == 6001 && e.voices[0].graph.Next() != 200 {
				t.Fatalf("block %d lost tied bend", block)
			}
		}
		if e.voices[0].graph.Next() != 0 || e.patterns[0].playingNote != 1 {
			t.Fatalf("block %d retriggered tie or lost reset", block)
		}
		if count := testing.AllocsPerRun(100, func() {
			e.Render(left[:block], right[:block])
			var message cmd.Message
			for e.Poll(&message) {
			}
		}); count != 0 {
			t.Fatalf("expression render allocated %v", count)
		}
	}
}

func TestPreloadedExpressionDoesNotShareCallerStorage(t *testing.T) {
	cfg := expressionConfig()
	pattern := seq.Pattern{Len: 1, GatePercent: 50, Expression: new([64]seq.Expression)}
	pattern.Expression[0] = seq.Expression{Set: true, Pressure: .25, Timbre: .5}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 69, Gate: true, Ratchet: 1, Probability: 100})
	cfg.Patterns = []PatternBank{{Slots: [16]seq.Pattern{pattern}}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Patterns[0].Slots[0].Expression[0].Pressure = 1
	if e.patterns[0].slots[0].ExpressionAt(0).Pressure != .25 {
		t.Fatal("preloaded expression retained caller-owned storage")
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	e.Render(left[:], right[:])
	if e.voices[0].graph.Next() != .25 {
		t.Fatal("caller mutation affected expression playback")
	}
}
