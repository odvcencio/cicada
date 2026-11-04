package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func TestSamplerLiveAllOffDoesNotReleasePerformedNote(t *testing.T) {
	pcm := make([]float32, 4096)
	for i := range pcm {
		pcm[i] = .2
	}
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 2, BPMMilli: 120000, Assets: []AudioAsset{{SampleRate: 48000, Left: pcm}}}
	cfg.Track[0] = TrackConfig{Kind: VoiceSample, Sample: &SamplerConfig{RootKey: 60, Voices: 2, Loop: true}}
	perf := &seq.Performance{LengthTicks: 3840, Notes: []seq.PerformedNote{{Tick: 0, EndTick: 1920, Note: 60, Velocity: 127, Expressions: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Pressure: .2, Timbre: .5}}}}}}
	e, err := New(performanceConfig(cfg, perf))
	if err != nil {
		t.Fatal(err)
	}
	var l, r [128]float32
	render := func() {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatal(m)
			}
		}
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	render()
	e.PushBatch([]cmd.Command{{Op: cmd.OpNoteOn, Track: 0, Index: 1, Arg0: 60 | 127<<8, Arg1: cmd.LiveIdentity}, {Op: cmd.OpNoteExpression, Track: 0, Index: 1, Arg1: math.Float32bits(.8), Pad: math.Float32bits(.5)}})
	for range 20 {
		render()
	}
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 65535, Arg1: cmd.LiveIdentity})
	for range 20 {
		render()
	}
	got, _ := e.voices[0].sampler.NextStereo()
	if math.Abs(float64(got-.24)) > .002 {
		t.Fatalf("live all-off changed performed note: %g", got)
	}
}

func TestSamplerLiveExpressionKeepsNoteOwnersIndependent(t *testing.T) {
	pcm := make([]float32, 4096)
	for i := range pcm {
		pcm[i] = .2
	}
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 2, BPMMilli: 120000}
	cfg.Assets = []AudioAsset{{SampleRate: 48000, Left: pcm}}
	cfg.Track[0] = TrackConfig{Kind: VoiceSample, Sample: &SamplerConfig{RootKey: 60, Voices: 2, Loop: true}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	render := func() {
		t.Helper()
		e.Render(left[:], right[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("engine fault: %+v", m)
			}
		}
	}
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpNoteOn, Index: 7, Arg0: 60 | 127<<8, Arg1: cmd.LiveIdentity},
		{Op: cmd.OpNoteOn, Index: 8, Arg0: 60 | 127<<8, Arg1: cmd.LiveIdentity},
		{Op: cmd.OpNoteExpression, Index: 7, Arg1: math.Float32bits(1), Pad: math.Float32bits(.5)},
	}) {
		t.Fatal("command queue full")
	}
	for i := 0; i < 40; i++ {
		render()
	}
	l, _ := e.voices[0].sampler.NextStereo()
	if math.Abs(float64(l)-.6) > .002 {
		t.Fatalf("independent sample pressure: got %g want .6", l)
	}
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Index: 7, Arg1: cmd.LiveIdentity})
	for i := 0; i < 3; i++ {
		render()
	}
	e.Push(cmd.Command{Op: cmd.OpNoteExpression, Index: 7, Arg1: math.Float32bits(1), Pad: math.Float32bits(.5)})
	render()
	l, _ = e.voices[0].sampler.NextStereo()
	if math.Abs(float64(l)-.2) > .002 {
		t.Fatalf("stale controls changed the other note: %g", l)
	}
	if got := testing.AllocsPerRun(100, render); got != 0 {
		t.Fatalf("sampler render allocated: %g", got)
	}
}
