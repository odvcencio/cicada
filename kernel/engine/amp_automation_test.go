package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/fx/pro"
	"math"
	"testing"
)

func TestAmpAutomationTrackOwnershipSeekLoop(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		cfg := sourceChainConfig(t)
		cfg.Tracks = 2
		cfg.MaxVoices = 2
		cfg.Track[1] = cfg.Track[0]
		cfg.Patterns = append(cfg.Patterns, cfg.Patterns[0])
		if scheduled {
			cfg.Schedule = LowerSong(cfg.Song)
			cfg.Song = nil
		}
		cfg.LoopSong = true
		amp := ampcab.DefaultParams()
		amp.Drive = .35
		eq := pro.Band{Enabled: true, Type: pro.Highpass, FrequencyHz: 80, Q: .707}
		cfg.Track[0].InsertChain = []TrackEffectConfig{{Kind: TrackEffectEQ, EQ: &eq}, {Kind: TrackEffectAmpCab, AmpCab: &amp}}
		if cfg.Tracks < 2 {
			t.Fatal("fixture requires two tracks")
		}
		cfg.Track[1].InsertChain = []TrackEffectConfig{{Kind: TrackEffectAmpCab, AmpCab: &amp}}
		cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAmpDrive), Arg0: math.Float32bits(.2), Tick: 4}, {Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAmpDrive), Arg0: math.Float32bits(.8), Tick: 8}}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		a, b := e.voices[0].chain.ampStage(), e.voices[1].chain.ampStage()
		if a == nil || b == nil {
			t.Fatal("missing amp")
		}
		e.meterRate = 0
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		var l, r [128]float32
		e.Render(l[:1], r[:1])
		if got := a.Params().Drive; got != .35 {
			t.Fatalf("initial %v", got)
		}
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 6})
		e.Render(l[:1], r[:1])
		if got := float32(a.Params().Drive); got != .2 {
			t.Fatalf("seek drive %g", got)
		}
		if got := b.Params().Drive; got != .35 {
			t.Fatalf("other track changed %g", got)
		}
		if !e.restartAutomationLanes() {
			t.Fatal("restart fault")
		}
		if got := float32(a.Params().Drive); got != .35 {
			t.Fatalf("loop baseline %g", got)
		}
		if e.TrackLatencyFrames() != ampcab.Latency {
			t.Fatal("latency changed")
		}
		if n := testing.AllocsPerRun(100, func() { e.Render(l[:], r[:]) }); n != 0 {
			t.Fatalf("render alloc %g", n)
		}
	}
}
