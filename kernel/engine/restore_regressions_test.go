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

// The engine's own loop wrap tells the host nothing, so it may change only the
// parameters the automation timeline moves: committed edits, previews, solo and
// mute fades and every unrelated parameter must come through untouched, while
// the automated parameter returns to the value it has before the first point.
func TestLoopWrapChangesOnlyAutomationLanes(t *testing.T) {
	for _, placement := range []bool{false, true} {
		for _, lane := range []struct {
			name      string
			first     int64
			want      float32 // pan after the wrap
			committed bool
		}{
			{"first point at zero", 0, .5, false},
			{"first point after zero", 600, 0, false},
			{"committed value of the automated parameter", 600, .3, true},
		} {
			t.Run(scheduleKindName(placement)+"/"+lane.name, func(t *testing.T) {
				cfg := restoreBase(t)
				cfg.Track[3].Solo = true // Piano soloed: track 0 stays excluded.
				cfg.LoopSong = true
				restoreScheduled(&cfg, placement)
				cfg.Automation = []cmd.Command{
					{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.5), Tick: lane.first},
					{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.9), Tick: 1200},
				}
				e, err := New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				e.meterRate = 0
				var l, r [128]float32
				e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
				for i := 0; i < 10; i++ {
					e.Render(l[:], r[:])
				}
				if lane.committed && !e.PushCommittedBatch([]cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.3)}}) {
					t.Fatal("committed edit refused")
				}
				// A committed edit and a preview on parameters with no lane.
				if !e.PushCommittedBatch([]cmd.Command{{Op: cmd.OpSetParam, Track: 4, Index: uint16(kernel.ParamKeysEpGain), Arg0: math.Float32bits(.2)}}) ||
					!e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAcidCutoff), Arg0: math.Float32bits(777)}) ||
					!e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixSendA), Arg0: math.Float32bits(.4)}) {
					t.Fatal("setup refused")
				}
				// Run well past the mute fade of the excluded track and past the lane.
				for frames := 1280; frames < 76_800-128; frames += 128 {
					e.Render(l[:], r[:])
				}
				if e.voices[0].muteGain != 0 {
					t.Fatalf("excluded track not faded out before the wrap: %g", e.voices[0].muteGain)
				}
				if e.voices[0].pan == lane.want {
					t.Fatalf("the lane never moved the pan before the wrap")
				}
				// Step through the wrap one frame at a time and check each one.
				sendA := e.voices[0].targetSendA
				for i := 0; i < 400; i++ {
					e.Render(l[:1], r[:1])
					if e.voices[0].muteGain != 0 {
						t.Fatalf("frame %d: the excluded track's mute gain became %g", i, e.voices[0].muteGain)
					}
					if e.voices[0].targetSendA != sendA {
						t.Fatalf("frame %d: an unrelated send changed to %g", i, e.voices[0].targetSendA)
					}
				}
				if e.voices[0].acidTarget.Cutoff != 777 {
					t.Fatalf("the preview on the cutoff was reset to %g", e.voices[0].acidTarget.Cutoff)
				}
				if e.voices[4].keys.(*controlKeys).values[10] != .2 {
					t.Fatalf("the committed keyboard gain was reset to %g", e.voices[4].keys.(*controlKeys).values[10])
				}
				if e.soloCount != 1 || !e.voices[3].soloed {
					t.Fatal("the authored solo was changed")
				}
				if e.voices[0].pan != lane.want {
					t.Fatalf("the automated pan is %g after the wrap, want %g", e.voices[0].pan, lane.want)
				}
				var message cmd.Message
				for e.Poll(&message) {
					if message.Kind == cmd.Fault {
						t.Fatalf("fault %d", message.A)
					}
				}
			})
		}
	}
}
