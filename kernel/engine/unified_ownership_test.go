package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func mixedOwnerConfig() Config {
	c := polyConfig()
	c.Tracks = 2
	c.MaxVoices = 6
	c.LoopSong = true
	c.Patterns = append(c.Patterns, PatternBank{})
	c.Patterns[1].Slots[0] = c.Patterns[0].Slots[0]
	clear(c.Patterns[1].Slots[0].Chords[:])
	c.Patterns[1].Slots[0].Steps[1], _ = seq.PackStep(seq.Step{Tie: true, Ratchet: 1, Probability: 100})
	c.Track[1] = TrackConfig{Kind: VoiceSample, Sample: &SamplerConfig{RootKey: 60, Voices: 2, Loop: true}}
	c.Assets = []AudioAsset{{SampleRate: 48000, Left: []float32{0.1, 0.05, -0.1, -0.05}}}
	c.Schedule = []ScheduleEvent{
		{Kind: SchedulePattern, Track: 0, Tick: 0, EndTick: 960, ID: 1},
		{Kind: SchedulePattern, Track: 1, Tick: 0, EndTick: 960, ID: 2},
		{Kind: SchedulePatternEnd, Track: 0, Tick: 960, EndTick: 960, ID: 1},
		{Kind: SchedulePatternEnd, Track: 1, Tick: 960, EndTick: 960, ID: 2},
	}
	return c
}

func TestUnifiedPoolOwnershipResetAndStaleHandles(t *testing.T) {
	e, err := New(mixedOwnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	var l, r [128]float32
	play := func() {
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
			t.Fatal("play rejected")
		}
		e.Render(l[:], r[:])
		if e.faulted {
			t.Fatal("mixed playback fault")
		}
	}
	play()
	if e.voices[0].poly == nil || e.voices[0].sampler != nil || e.voices[1].sampler == nil || e.voices[1].poly != nil {
		t.Fatal("graph and scalar sampler owners conflated")
	}
	if e.voices[0].poly.ActiveVoices() != 3 || e.voices[1].sampler.ActiveVoices() != 1 {
		t.Fatal("mixed owners not playing")
	}
	stale := e.voices[1].samplerNote
	e.Reset()
	if e.voices[0].poly.ActiveVoices() != 0 || e.voices[1].sampler.ActiveVoices() != 0 {
		t.Fatal("reset leaked owner")
	}
	play()
	if e.voices[1].samplerNote.ID <= stale.ID || e.voices[1].sampler.NoteOff(stale) {
		t.Fatal("stale sampler handle released replacement")
	}
	e.noteOff(0, 0xffff)
	if e.voices[1].sampler.ActiveVoices() != 1 {
		t.Fatal("graph release changed sampler owner")
	}
	if !e.Push(cmd.Command{Op: cmd.OpStop, Track: 255}) {
		t.Fatal("stop rejected")
	}
	for i := 0; i < 500; i++ {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatal(m)
			}
		}
	}
	if e.voices[0].poly.ActiveVoices() != 0 || e.voices[1].sampler.ActiveVoices() != 0 {
		t.Fatal("stop leaked graph or sampler release")
	}
}

func TestUnifiedMixedScheduleCallbackAllocationFree(t *testing.T) {
	e, err := New(mixedOwnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("play rejected")
	}
	var l, r [128]float32
	e.Render(l[:], r[:])
	allocations := testing.AllocsPerRun(1000, func() {
		e.Render(l[:], r[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				panic("mixed callback fault")
			}
		}
	})
	if allocations != 0 {
		t.Fatalf("mixed schedule callback allocated %g", allocations)
	}
}
