package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func placementTempoConfig() Config {
	c := sceneTempoConfig()
	c.Song = nil
	c.Scenes = nil
	c.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: 0}}
	c.Schedule = []ScheduleEvent{
		{Kind: ScheduleTempo, Tick: 0, EndTick: 0, ID: 96000},
		{Kind: SchedulePattern, Tick: 0, EndTick: 6 * 3840, ID: 1},
		{Kind: ScheduleTempo, Tick: 2 * 3840, EndTick: 2 * 3840, ID: 60000},
		{Kind: ScheduleTempo, Tick: 4 * 3840, EndTick: 4 * 3840, ID: 120000},
		{Kind: SchedulePatternEnd, Tick: 6 * 3840, EndTick: 6 * 3840, ID: 1},
	}
	return c
}

func TestPlacementTempoSeekLoopAndRenderBlockIndependence(t *testing.T) {
	cfg := placementTempoConfig()
	cfg.LoopSong = true
	var reference []float32
	for _, block := range []int{64, 128, 511} {
		cfg.MaxBlock = block
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		const cycle = 2*120000 + 2*192000 + 2*96000
		if got := e.FramesBetweenTicks(0, 6*3840); got != cycle {
			t.Fatal("cycle duration", got)
		}
		if got := e.FramesBetweenTicks(0, 12*3840); got != cycle*2 {
			t.Fatal("loop duration", got)
		}
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		l, r := make([]float32, block), make([]float32, block)
		out := make([]float32, cycle+256)
		for at := 0; at < len(out); {
			n := min(block, len(out)-at)
			e.Render(l[:n], r[:n])
			copy(out[at:], l[:n])
			at += n
		}
		if e.transport.BPMMilli() != 96000 || e.transport.Tick() < 6*3840 {
			t.Fatal("loop failed to restore first tempo", e.transport.Clock())
		}
		if reference == nil {
			reference = out
		} else {
			for i := range out {
				if reference[i] != out[i] {
					t.Fatal("block-dependent tempo audio", block, i)
				}
			}
		}
		if !e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 3, Arg1: 137}) {
			t.Fatal("seek rejected")
		}
		e.Render(l[:1], r[:1])
		if e.transport.BPMMilli() != 60000 {
			t.Fatal("seek tempo", e.transport.Clock())
		}
		if n := testing.AllocsPerRun(50, func() { e.FramesBetweenTicks(0, 12*seq.TicksPerBar); e.Render(l, r) }); n != 0 {
			t.Fatal("callback allocated", n)
		}
		e.Close()
	}
}

func TestPlacementTempoRejectsMalformedSchedules(t *testing.T) {
	for _, bad := range []ScheduleEvent{{Kind: ScheduleTempo, ID: 19999}, {Kind: ScheduleTempo, ID: 300001}, {Kind: ScheduleTempo, ID: 120000, Tick: 240, EndTick: 240}, {Kind: ScheduleTempo, ID: 120000, Track: 1}, {Kind: ScheduleTempo, ID: 120000, EndTick: 1}} {
		cfg := placementTempoConfig()
		cfg.Schedule[0] = bad
		if e, err := New(cfg); err == nil {
			e.Close()
			t.Fatal("accepted", bad)
		}
	}
}
