package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// restoreScheduled adds a two-bar song or one arrange placement to a config.
func restoreScheduled(cfg *Config, placement bool) {
	if placement {
		cfg.Patterns = make([]PatternBank, restoreTracks)
		cfg.Patterns[1].Drums = new([16][drum.LaneCount]seq.Pattern)
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 50}
		cfg.Schedule = []ScheduleEvent{
			{Kind: SchedulePattern, Track: 0, Index: 0, Tick: 0, EndTick: 7680, ID: 1},
			{Kind: SchedulePatternEnd, Track: 0, Tick: 7680, EndTick: 7680, ID: 1},
		}
		return
	}
	cfg.Scenes = []Scene{{}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 2}}
}

// restartAndSound rewinds a moved engine in the given way, starts the same note
// on a fresh engine and on the rewound one, and returns both outputs.
func restartAndSound(t *testing.T, cfg Config, setup func(*Engine), restart string) (got, want [2048]float32, e, fresh *Engine) {
	t.Helper()
	build := func() *Engine {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		return e
	}
	var l, r [128]float32
	e, fresh = build(), build()
	for _, x := range []*Engine{e, fresh} {
		x.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	}
	setup(e)
	frames := 0
	for ; frames < 9600; frames += 128 { // Past the lane's point at tick 600.
		e.Render(l[:], r[:])
	}
	switch restart {
	case "seek":
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255})
	case "loop":
		for ; frames < 76_800; frames += 128 {
			e.Render(l[:], r[:])
		}
	}
	setup(fresh)
	for _, x := range []*Engine{e, fresh} {
		x.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8})
	}
	for i := 0; i < len(got); i += 128 {
		e.Render(l[:], r[:])
		copy(got[i:], l[:])
		fresh.Render(l[:], r[:])
		copy(want[i:], l[:])
	}
	return
}

// An authored solo is part of the authored state: an unrelated automation lane
// must not turn it off after a seek or a loop, in song and arrange schedules.
func TestAutomationRestartKeepsAuthoredSolo(t *testing.T) {
	for _, placement := range []bool{false, true} {
		for _, restart := range []string{"seek", "loop"} {
			t.Run(scheduleKindName(placement)+"/"+restart, func(t *testing.T) {
				cfg := restoreBase(t)
				cfg.Track[3].Solo = true // The piano is soloed; the acid track is not.
				cfg.LoopSong = restart == "loop"
				restoreScheduled(&cfg, placement)
				cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.5), Tick: 600}}
				got, want, e, fresh := restartAndSound(t, cfg, func(*Engine) {}, restart)
				if e.soloCount != 1 || !e.voices[3].soloed || e.voices[0].muteTarget != 0 {
					t.Fatalf("solo state lost: count %d, piano soloed %v, acid mute target %g", e.soloCount, e.voices[3].soloed, e.voices[0].muteTarget)
				}
				if engineState(e) != engineState(fresh) {
					t.Fatal("restarted state differs from a fresh engine's")
				}
				var peak float64
				for i := range got {
					if restart == "seek" && math.Abs(float64(got[i]-want[i])) > 1e-4 { // A loop keeps oscillator phase, so only state is compared there.
						t.Fatalf("frame %d: %g, fresh engine has %g", i, got[i], want[i])
					}
					if i >= 1024 { // After the mute fade.
						peak = math.Max(peak, math.Abs(float64(got[i])))
					}
				}
				if peak > 1e-4 {
					t.Fatalf("the unsoloed track sounds after the restart (peak %g)", peak)
				}
			})
		}
	}
}

// A positioned track is scaled by its own gain, not the stereo mix: a gain lane
// that has moved it must not leave the old gain after a seek or a loop.
func TestAutomationRestartRestoresPositionedGain(t *testing.T) {
	for _, placement := range []bool{false, true} {
		for _, restart := range []string{"seek", "loop"} {
			t.Run(scheduleKindName(placement)+"/"+restart, func(t *testing.T) {
				cfg := restoreBase(t)
				cfg.LoopSong = restart == "loop"
				restoreScheduled(&cfg, placement)
				cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-20), Tick: 600}}
				position := func(e *Engine) {
					e.Push(cmd.Command{Op: cmd.OpSetTrackPosition, Track: 0, Index: 1, Arg0: math.Float32bits(1), Pad: math.Float32bits(1)})
				}
				got, want, e, fresh := restartAndSound(t, cfg, position, restart)
				if e.spatial.tracks[0].gain != fresh.spatial.tracks[0].gain || e.spatial.tracks[0].target != fresh.spatial.tracks[0].target {
					t.Fatalf("positioned gain %g/%g, a fresh engine has %g/%g", e.spatial.tracks[0].gain, e.spatial.tracks[0].target, fresh.spatial.tracks[0].gain, fresh.spatial.tracks[0].target)
				}
				var peak float64
				for i := range got {
					if restart == "seek" && math.Abs(float64(got[i]-want[i])) > 1e-4 {
						t.Fatalf("positioned frame %d: %g, fresh engine has %g", i, got[i], want[i])
					}
					peak = math.Max(peak, math.Abs(float64(got[i])))
				}
				if restart == "seek" && peak == 0 {
					t.Fatal("no positioned sound to compare")
				}
			})
		}
	}
}
