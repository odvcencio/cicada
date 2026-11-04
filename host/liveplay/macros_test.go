package liveplay

import (
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

func TestNamedMacroControlsReachThePlayingEngine(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.MacroMappings = []engine.MacroMapping{{Macro: 0, Track: 0, Param: uint16(kernel.ParamAcidCutoff), Min: 200, Max: 4000, Exponential: true}}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !eng.Push(cmd.Command{Op: cmd.OpDefineMacro, Track: 255, Index: 0, Arg0: math.Float32bits(0)}) {
		t.Fatal("define macro")
	}
	p, err := New(Score{Engine: eng, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "bass"}}, Macros: []MacroControl{{Name: "filter", SmoothFrames: 256}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pcm := make([]byte, blockFrames*8)
	if _, err := io.ReadFull(p, pcm); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMacro("unknown", .5); err == nil {
		t.Fatal("accepted unknown macro")
	}
	if err := p.SetMacro("filter", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(p, pcm); err != nil {
		t.Fatal(err)
	}
	if got := p.MacroTargets()["filter"]; got != 1 {
		t.Fatalf("target = %g", got)
	}
	if _, err := io.ReadFull(p, pcm); err != nil {
		t.Fatal(err)
	}
	if current, _ := eng.MacroValue(0); current != 1 {
		t.Fatalf("smoothed value = %g", current)
	}
	var readErr error
	if allocations := testing.AllocsPerRun(20, func() { _, readErr = p.Read(pcm) }); readErr != nil || allocations != 0 {
		t.Fatalf("render with a mapped macro: allocations=%g err=%v", allocations, readErr)
	}
}

func TestNamedMacroFollowsItsNameAcrossScoreReplacement(t *testing.T) {
	build := func(names []MacroControl, mapped uint8) Score {
		cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
		cfg.Track[0].Kind = engine.VoiceAcid
		cfg.MacroMappings = []engine.MacroMapping{{Macro: mapped, Track: 0, Param: uint16(kernel.ParamAcidCutoff), Min: 200, Max: 4000}}
		eng, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return Score{Engine: eng, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "bass"}}, Macros: names}
	}
	first := build([]MacroControl{{Name: "filter"}}, 0)
	p, err := New(first, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.SetMacro("filter", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 1024*8); err != nil {
		t.Fatal(err)
	}
	next := build([]MacroControl{{Name: "other"}, {Name: "filter"}}, 1)
	if err := p.Offer(next); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 100000*8); err != nil {
		t.Fatal(err)
	}
	if got := p.MacroTargets()["filter"]; got != 1 {
		t.Fatalf("retained target = %g", got)
	}
	if current, _ := next.Engine.MacroValue(1); current != 1 {
		t.Fatalf("replacement macro = %g", current)
	}
	if current, _ := next.Engine.MacroValue(0); current != 0 {
		t.Fatalf("unrelated replacement macro = %g", current)
	}
}
