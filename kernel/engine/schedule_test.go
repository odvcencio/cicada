package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func TestScheduleMatchesLegacySong(t *testing.T) {
	cfg := testConfig()
	cfg.BPMMilli = 137123
	cfg.Scenes = []Scene{{Track: [16]SceneBinding{{Mode: SceneSlot}}}, {Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 1}}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 0, Bars: 2}, {Scene: 1, Bars: 1}}
	for _, loop := range []bool{false, true} {
		cfg.LoopSong = loop
		legacy, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		scheduled := cfg
		scheduled.Schedule, scheduled.Song = LowerSong(cfg.Song), nil
		timeline, err := New(scheduled)
		if err != nil {
			t.Fatal(err)
		}
		commands := []cmd.Command{{Op: cmd.OpPlay, Track: 0xff}}
		legacy.PushBatch(commands)
		timeline.PushBatch(commands)
		var al, ar, bl, br [128]float32
		var message cmd.Message
		for block := 0; block < 5000; block++ {
			if block == 700 || block == 1800 {
				c := cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(block / 700), Arg1: 137}
				legacy.Push(c)
				timeline.Push(c)
			}
			legacy.Render(al[:], ar[:])
			timeline.Render(bl[:], br[:])
			if al != bl || ar != br || legacy.transport.Tick() != timeline.transport.Tick() || legacy.songIndex != timeline.songIndex || legacy.faulted || timeline.faulted {
				t.Fatalf("schedule drift at block %d (loop %v)", block, loop)
			}
			for legacy.Poll(&message) {
			}
			for timeline.Poll(&message) {
			}
		}
	}
}

func TestScheduleRenderSeekAllocationFree(t *testing.T) {
	cfg := testConfig()
	cfg.Scenes = []Scene{{Track: [16]SceneBinding{{Mode: SceneSlot}}}}
	cfg.Schedule = []ScheduleEvent{{Tick: 0, EndTick: seq.TicksPerBar, Scene: 0}}
	cfg.LoopSong = true
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	var message cmd.Message
	allocs := testing.AllocsPerRun(100, func() {
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: uint32(seq.TicksPerBar - 1)})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		e.Render(left[:], right[:])
		for e.Poll(&message) {
		}
	})
	if allocs != 0 || e.faulted {
		t.Fatalf("allocations=%g fault=%v", allocs, e.faulted)
	}
}

func TestScheduleRejectsAmbiguousAndInvalidAuthority(t *testing.T) {
	for _, events := range [][]ScheduleEvent{{{Tick: 1, EndTick: 10}}, {{EndTick: 0}}, {{EndTick: 10, Scene: 1}}, {{EndTick: 10}, {Tick: 9, EndTick: 20}}} {
		cfg := testConfig()
		cfg.Scenes = []Scene{{}}
		cfg.Schedule = events
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", events)
		}
	}
	cfg := testConfig()
	cfg.Scenes = []Scene{{}}
	cfg.Song = []SongEntry{{Bars: 1}}
	cfg.Schedule = LowerSong(cfg.Song)
	if _, err := New(cfg); err == nil {
		t.Fatal("accepted two arrangement authorities")
	}
}
