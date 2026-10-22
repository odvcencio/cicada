package engine

import (
	"slices"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

// Two overlapping clips share a track and asset. The reference plays the full
// schedule with silent PCM for the unwanted clip; the listening engine keeps
// the original assets and suppresses that placement's starts and releases.
func TestPlacementListeningRetainsClockAndIsolatesOverlappingClips(t *testing.T) {
	for _, block := range []int{64, 128, 511} {
		cfg := clipScheduleConfig()
		cfg.MaxBlock = block
		cfg.BPMMilli = 137333
		cfg.Schedule = []ScheduleEvent{
			{Kind: ScheduleTempo, ID: 137333},
			{Kind: ScheduleClip, Tick: 0, EndTick: 4 * seq.TicksPerBar, ID: 1},
			{Kind: ScheduleClip, Tick: 240, EndTick: 960, ID: 2},
			{Kind: ScheduleClipEnd, Tick: 960, EndTick: 960, ID: 2},
			{Kind: ScheduleTempo, Tick: seq.TicksPerBar, EndTick: seq.TicksPerBar, ID: 96000},
			{Kind: ScheduleTempo, Tick: 3 * seq.TicksPerBar, EndTick: 3 * seq.TicksPerBar, ID: 180000},
			{Kind: ScheduleClipEnd, Tick: 4 * seq.TicksPerBar, EndTick: 4 * seq.TicksPerBar, ID: 1},
		}
		a, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.SetPlaybackPlacement(2); err != nil {
			t.Fatal(err)
		}
		reference := cfg
		reference.Assets = append(slices.Clone(cfg.Assets), AudioAsset{Left: make([]float32, len(cfg.Assets[0].Left)), SampleRate: 48000})
		reference.Clips = append(slices.Clone(cfg.Clips), ClipConfig{Asset: 1, EndFrame: int64(len(cfg.Assets[0].Left))})
		reference.Schedule = slices.Clone(cfg.Schedule)
		reference.Schedule[1].Index = 1
		b, err := New(reference)
		if err != nil {
			t.Fatal(err)
		}
		if a.FramesBetweenTicks(0, 4*seq.TicksPerBar) != b.FramesBetweenTicks(0, 4*seq.TicksPerBar) || a.TempoAtTick(3*seq.TicksPerBar) != 180000 {
			t.Fatal("solo changed the full song clock")
		}
		var audible bool
		al, ar, bl, br := make([]float32, block), make([]float32, block), make([]float32, block), make([]float32, block)
		for _, tick := range []int64{0, 480, 800} {
			for _, e := range []*Engine{a, b} {
				e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: uint32(tick)})
				e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			}
			for rendered := 0; rendered < 30000; rendered += block {
				a.Render(al, ar)
				b.Render(bl, br)
				if !slices.Equal(al, bl) || !slices.Equal(ar, br) {
					t.Fatal("solo differs from silent-asset reference", block, tick, rendered)
				}
				for _, v := range al {
					audible = audible || v != 0
				}
			}
		}
		if !audible {
			t.Fatal("selected clip was silent")
		}
		if err := a.SetPlaybackPlacement(1); err == nil {
			t.Fatal("changed listening ownership after rendering")
		}
		if n := testing.AllocsPerRun(20, func() { a.Render(al, ar) }); n != 0 {
			t.Fatal("solo callback allocated", n)
		}
		a.Close()
		b.Close()
	}
}

func TestPlacementListeningDoesNotHandOffToAnUnselectedPattern(t *testing.T) {
	cfg := handoffLoopConfig(t)
	cfg.LoopSong = false
	bar := int64(seq.TicksPerBar)
	cfg.Schedule = []ScheduleEvent{{Kind: SchedulePattern, EndTick: bar, ID: 1}, {Kind: SchedulePatternEnd, Tick: bar, EndTick: bar, ID: 1}, {Kind: SchedulePattern, Tick: bar, EndTick: 2 * bar, ID: 2}, {Kind: SchedulePatternEnd, Tick: 2 * bar, EndTick: 2 * bar, ID: 2}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.SetPlaybackPlacement(3); err == nil {
		t.Fatal("unknown placement accepted")
	}
	if err := e.SetPlaybackPlacement(1); err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var left, right [128]float32
	for rendered, end := int64(0), e.FramesBetweenTicks(0, bar)+128; rendered < end; rendered += 128 {
		e.Render(left[:], right[:])
	}
	if e.patterns[0].active != -1 || e.placementID[0] != 1 {
		t.Fatal("unselected pattern inherited the selected placement's held note")
	}
}
