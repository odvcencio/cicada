package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func handoffLoopConfig(t *testing.T) Config {
	t.Helper()
	pack := func(s seq.Step) uint32 {
		v, err := seq.PackStep(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, LoopSong: true, Seed: 7}
	cfg.Track[0].Kind = VoiceAcid
	cfg.Patterns = []PatternBank{{}}
	pattern := seq.Pattern{Len: 16, GatePercent: 80, Seed: cfg.Seed}
	for i := range 16 {
		pattern.Steps[i] = pack(seq.Step{Ratchet: 1, Probability: 100})
	}
	pattern.Steps[0] = pack(seq.Step{Note: 48, Gate: true, Ratchet: 1, Probability: 100})
	pattern.Steps[15] = pack(seq.Step{Note: 55, Gate: true, Slide: true, Ratchet: 1, Probability: 100})
	cfg.Patterns[0].Slots[0] = pattern
	return cfg
}

// A looping arrangement that wraps from its last placement end to a placement
// at tick 0 on the same track hands off like a looping song of the same scene.
func TestPlacementLoopWrapHandsOffLikeSongLoop(t *testing.T) {
	song := handoffLoopConfig(t)
	song.Scenes = []Scene{{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot}}}}
	song.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 0, Bars: 1}}
	placed := handoffLoopConfig(t)
	bar := int64(seq.TicksPerBar)
	placed.Schedule = []ScheduleEvent{
		{Kind: SchedulePattern, Tick: 0, EndTick: bar, ID: 1},
		{Kind: SchedulePatternEnd, Tick: bar, EndTick: bar, ID: 1},
		{Kind: SchedulePattern, Tick: bar, EndTick: 2 * bar, ID: 2},
		{Kind: SchedulePatternEnd, Tick: 2 * bar, EndTick: 2 * bar, ID: 2},
	}
	a, err := New(song)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(placed)
	if err != nil {
		t.Fatal(err)
	}
	a.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	b.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var al, ar, bl, br [128]float32
	var message cmd.Message
	for block := 0; block < 3*2*int(bar)*48000/(960*2)/128+1; block++ {
		a.Render(al[:], ar[:])
		b.Render(bl[:], br[:])
		if al != bl || ar != br {
			t.Fatalf("loop wrap differs from song loop at block %d", block)
		}
		for a.Poll(&message) {
		}
		for b.Poll(&message) {
		}
	}
}

// A looping arrangement that hands off at the wrap keeps its notes and tails
// when a lane also restarts there: the output matches the same arrangement
// without the lane, and the lane's parameter returns to its authored value.
func TestPlacementLoopHandoffKeepsTailsAndRestartsLanes(t *testing.T) {
	bar := int64(seq.TicksPerBar)
	build := func(lane bool) Config {
		cfg := handoffLoopConfig(t)
		cfg.Tracks = 2
		cfg.Track[1].Kind = VoiceAcid // A silent track the lane moves, so the audio stays comparable.
		cfg.Patterns = append(cfg.Patterns, PatternBank{})
		cfg.Schedule = []ScheduleEvent{
			{Kind: SchedulePattern, Tick: 0, EndTick: bar, ID: 1},
			{Kind: SchedulePatternEnd, Tick: bar, EndTick: bar, ID: 1},
			{Kind: SchedulePattern, Tick: bar, EndTick: 2 * bar, ID: 2},
			{Kind: SchedulePatternEnd, Tick: 2 * bar, EndTick: 2 * bar, ID: 2},
		}
		cfg.MaxVoices = 2
		if lane {
			cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 1, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.5), Tick: 600}}
		}
		return cfg
	}
	plain, err := New(build(false))
	if err != nil {
		t.Fatal(err)
	}
	laned, err := New(build(true))
	if err != nil {
		t.Fatal(err)
	}
	plain.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	laned.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var al, ar, bl, br [128]float32
	cycle := 2 * int(bar) * 48000 / (960 * 2) // Frames in two bars at 120 BPM.
	movedBeforeWrap, restartedAfterWrap := false, false
	for block := 0; block < 2*cycle/128+40; block++ {
		plain.Render(al[:], ar[:])
		laned.Render(bl[:], br[:])
		if al != bl || ar != br {
			t.Fatalf("block %d: the lane changed the output, so a tail or note was lost at the wrap", block)
		}
		frames := (block + 1) * 128
		if frames > cycle-2000 && frames < cycle && laned.voices[1].pan == .5 {
			movedBeforeWrap = true
		}
		if frames > cycle+128 && frames < cycle+3000 && laned.voices[1].pan == 0 {
			restartedAfterWrap = true
		}
	}
	if !movedBeforeWrap || !restartedAfterWrap {
		t.Fatalf("lane pan moved before the wrap %v, returned to authored after it %v", movedBeforeWrap, restartedAfterWrap)
	}
}
