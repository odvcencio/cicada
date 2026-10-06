package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/piano"
)

func pianoConfig() Config {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: piano.MaxVoices, BPMMilli: 120_000}
	cfg.Track[0].Kind = VoicePiano
	return cfg
}

func TestPianoVoiceBudgetAndRenderAllocations(t *testing.T) {
	cfg := pianoConfig()
	cfg.MaxVoices--
	if _, err := New(cfg); err == nil {
		t.Fatal("piano must reserve its full polyphony")
	}
	e, err := New(pianoConfig())
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	var message cmd.Message
	var fault uint16
	allocations := testing.AllocsPerRun(50, func() {
		for _, note := range []uint32{48, 60, 64, 67} {
			e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: note | 96<<8})
		}
		e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamPianoSustain), Arg0: math.Float32bits(.7)})
		e.Render(left[:], right[:])
		e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 0xffff})
		e.Render(left[:], right[:])
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				fault = message.A
			}
		}
	})
	if allocations != 0 || e.faulted {
		t.Fatalf("piano render allocated %g times, fault=%v code=%d", allocations, e.faulted, fault)
	}
}

func TestPianoLiveNoteOffTargetsOneKey(t *testing.T) {
	first, err := New(pianoConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(pianoConfig())
	if err != nil {
		t.Fatal(err)
	}
	var aL, aR, bL, bR [128]float32
	for _, e := range []*Engine{first, second} {
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 64 | 100<<8})
	}
	for range 8 {
		first.Render(aL[:], aR[:])
		second.Render(bL[:], bR[:])
	}
	first.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 60})
	second.apply(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 64})
	different, sounded := false, false
	for range 16 {
		first.Render(aL[:], aR[:])
		second.Render(bL[:], bR[:])
		for i := range aL {
			different = different || aL[i] != bL[i] || aR[i] != bR[i]
			sounded = sounded || aL[i] != 0 || aR[i] != 0
		}
	}
	if first.faulted || second.faulted || !different || !sounded {
		t.Fatal("targeted releases must leave different chord keys ringing")
	}
}

func TestPianoTransposedPatternMatchesLiveCommands(t *testing.T) {
	cfg := pianoConfig()
	pattern := seq.Pattern{Len: 4, GatePercent: 50, Transpose: 12}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 48, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = pattern
	patternEngine, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	liveEngine, err := New(pianoConfig())
	if err != nil {
		t.Fatal(err)
	}
	patternEngine.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	patternEngine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	liveEngine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	liveEngine.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
	liveEngine.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 60, Tick: seq.TicksPerStep / 2})
	var aL, aR, bL, bR [128]float32
	for block := range 140 {
		patternEngine.Render(aL[:], aR[:])
		liveEngine.Render(bL[:], bR[:])
		if aL != bL || aR != bR {
			t.Fatalf("transposed piano release differs from live release in block %d", block)
		}
	}
	if patternEngine.faulted || liveEngine.faulted {
		t.Fatal("piano pattern or live commands faulted")
	}
}

func TestPianoSceneSeekRestoresSustain(t *testing.T) {
	cfg := pianoConfig()
	cfg.Track[0].PianoSustain = .25
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: 0, ID: kernel.ParamPianoSustain, Value: 1}}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1})
	if e.faulted || e.voices[0].pianoSustain != 1 {
		t.Fatal("forward seek did not apply sustain")
	}
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	if e.faulted || e.voices[0].pianoSustain != .25 {
		t.Fatal("backward seek retained a later sustain setting")
	}
}

func TestPianoUnrelatedLiveReleasePreservesPatternGate(t *testing.T) {
	cfg := pianoConfig()
	pattern := seq.Pattern{Len: 4, GatePercent: 50}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = pattern
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []*Engine{first, second} {
		e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	}
	var aL, aR, bL, bR [128]float32
	first.Render(aL[:], aR[:])
	second.Render(bL[:], bR[:])
	first.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 64})
	for block := range 80 {
		first.Render(aL[:], aR[:])
		second.Render(bL[:], bR[:])
		if aL != bL || aR != bR || first.faulted || second.faulted {
			t.Fatalf("unrelated key release changed the pattern gate in block %d", block)
		}
	}
}

func TestPianoRejectsInvalidLiveKeysAndPatternChanges(t *testing.T) {
	for _, command := range []cmd.Command{
		{Op: cmd.OpNoteOn, Track: 0, Arg0: 20 | 100<<8},
		{Op: cmd.OpNoteOff, Track: 0, Index: 109},
	} {
		e, err := New(pianoConfig())
		if err != nil {
			t.Fatal(err)
		}
		e.apply(command)
		if !e.faulted {
			t.Fatal("piano accepted a live key outside MIDI 21 to 108")
		}
	}
	cfg := pianoConfig()
	pattern := seq.Pattern{Len: 1, GatePercent: 55}
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 20, Gate: true, Ratchet: 1, Probability: 100})
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = pattern
	if _, err := New(cfg); err == nil {
		t.Fatal("piano accepted an invalid preloaded key")
	}
	e, err := New(pianoConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.apply(cmd.Command{Op: cmd.OpSetStep, Track: 0, Arg0: pattern.Steps[0]})
	if !e.faulted {
		t.Fatal("piano accepted an invalid live pattern key")
	}
}
