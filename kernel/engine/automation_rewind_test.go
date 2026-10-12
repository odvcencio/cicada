package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// Automation of non-keyboard parameters (mixer pan, acid cutoff) rewinds like
// fresh playback after a seek to the start, a seek to the middle, a loop
// restart and a replay after the song ended.
func TestAutomationRewindsLikeFreshPlayback(t *testing.T) {
	for _, placement := range []bool{false, true} {
		t.Run(scheduleKindName(placement), func(t *testing.T) { automationRewindsLikeFreshPlayback(t, placement) })
	}
}

func scheduleKindName(placement bool) string {
	if placement {
		return "placement"
	}
	return "song"
}

// rewindConfig is the two-bar source-chain song, or the same length as one
// placement of its first pattern (an arrange schedule).
func rewindConfig(t *testing.T, placement bool) Config {
	cfg := sourceChainConfig(t)
	if placement {
		cfg.Song = nil
		cfg.Schedule = []ScheduleEvent{
			{Kind: SchedulePattern, Track: 0, Index: 0, Tick: 0, EndTick: 7680, ID: 1},
			{Kind: SchedulePatternEnd, Track: 0, Tick: 7680, EndTick: 7680, ID: 1},
		}
	}
	return cfg
}

func automationRewindsLikeFreshPlayback(t *testing.T, placement bool) {
	type lane struct {
		name  string
		track uint16
		id    kernel.ParamID
		value func(*Engine) float32
		pts   []float32 // values at ticks 0 (or later), 600 and 1200
	}
	lanes := []lane{
		{"mixer pan", 0, kernel.ParamMixPan, func(e *Engine) float32 { return e.voices[0].pan }, []float32{.5, .9, .1}},
		{"acid cutoff", 0, kernel.ParamAcidCutoff, func(e *Engine) float32 { return float32(e.voices[0].acidTarget.Cutoff) }, []float32{900, 3000, 400}},
	}
	for _, l := range lanes {
		for _, first := range []int64{0, 4} {
			cfg := rewindConfig(t, placement)
			if cfg.Track[0].Kind != VoiceAcid && l.id == kernel.ParamAcidCutoff {
				t.Skip("fixture track 0 is not acid")
			}
			cfg.LoopSong = false
			for i, tick := range []int64{first, 600, 1200} {
				cfg.Automation = append(cfg.Automation, cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(l.id), Arg0: math.Float32bits(l.pts[i]), Tick: tick})
			}
			fresh := func() *Engine {
				e, err := New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				e.meterRate = 0
				return e
			}
			var left, right [128]float32
			runTo := func(e *Engine, tick int64) {
				for e.transport.Tick() < tick {
					e.Render(left[:], right[:])
				}
			}
			reference := fresh()
			authored := l.value(reference)
			// Reference values of an uninterrupted fresh playback.
			reference.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
			reference.Render(left[:1], right[:1])
			atStart := l.value(reference)
			runTo(reference, 800)
			atMiddle := l.value(reference)
			runTo(reference, 1300)
			atEnd := l.value(reference)
			if atMiddle == atEnd || atMiddle == authored {
				t.Fatalf("%s: weak fixture (%g %g %g)", l.name, authored, atMiddle, atEnd)
			}
			e := fresh()
			e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
			runTo(e, 1300)
			check := func(moment string, want float32) {
				t.Helper()
				if got := l.value(e); got != want {
					t.Fatalf("%s first=%d %s: %g, fresh playback has %g", l.name, first, moment, got, want)
				}
			}
			// Seek to the start.
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255})
			e.Render(left[:1], right[:1])
			want := authored
			if first == 0 {
				want = atStart
			}
			check("after a seek to the start", want)
			runTo(e, 800)
			check("replaying to the middle", atMiddle)
			runTo(e, 1300)
			check("replaying to the end", atEnd)
			// Seek to the middle (tick 800 = bar 0, 800 ticks).
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 800})
			e.Render(left[:1], right[:1])
			check("after a seek to the middle", atMiddle)
			runTo(e, 1300)
			check("replaying from the middle to the end", atEnd)
		}
	}
}

// A loop restart and a replay after the song ended begin automation like fresh
// playback does: the lane's parameter returns to its authored value (or the
// tick-0 point) and the lane runs again.
func TestAutomationLoopRestartAndReplayAfterEnd(t *testing.T) {
	for _, placement := range []bool{false, true} {
		t.Run(scheduleKindName(placement), func(t *testing.T) { automationLoopRestartAndReplayAfterEnd(t, placement) })
	}
}

func automationLoopRestartAndReplayAfterEnd(t *testing.T, placement bool) {
	for _, looped := range []bool{true, false} {
		for _, first := range []int64{0, 4} {
			cfg := rewindConfig(t, placement)
			cfg.LoopSong = looped
			for i, tick := range []int64{first, 600, 1200} {
				cfg.Automation = append(cfg.Automation, cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits([]float32{.5, .9, .1}[i]), Tick: tick})
			}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.meterRate = 0
			authored := e.voices[0].pan
			want := authored
			if first == 0 {
				want = .5
			}
			var left, right [128]float32
			e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
			e.Render(left[:1], right[:1])
			if looped {
				// Run past the end of the song and stop right after the wrap.
				previous := e.transport.Tick()
				for i := 0; i < 400000; i++ {
					if previous%7680 < 7600 {
						e.Render(left[:], right[:])
					} else {
						e.Render(left[:1], right[:1])
					}
					if e.transport.Tick()%7680 < previous%7680 {
						break
					}
					previous = e.transport.Tick()
				}
				if e.transport.Tick()%7680 >= previous%7680 {
					t.Fatal("the song never looped")
				}
				e.Render(left[:1], right[:1]) // The wrap itself lands on the next frame.
			} else {
				for i := 0; i < 100000 && e.transport.Playing(); i++ {
					e.Render(left[:], right[:])
				}
				if e.transport.Playing() {
					t.Fatal("the song never ended")
				}
				if e.voices[0].pan != .1 {
					t.Fatalf("automation did not reach its end value: %g", e.voices[0].pan)
				}
				e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
				e.Render(left[:1], right[:1])
			}
			if got := e.voices[0].pan; got != want {
				t.Fatalf("loop=%v first=%d: restart value %g, fresh playback has %g", looped, first, got, want)
			}
			for e.transport.Tick()%7680 < 800 && e.transport.Tick()%7680 >= 0 && (e.transport.Tick()%7680 < 800) {
				e.Render(left[:], right[:])
				if e.transport.Tick()%7680 >= 800 {
					break
				}
			}
			if got := e.voices[0].pan; got != .9 {
				t.Fatalf("loop=%v first=%d: automation did not run again (%g at tick %d)", looped, first, got, e.transport.Tick())
			}
		}
	}
}

// A scene launch and a director state change do not jump the timeline: the
// automated value stays and the lane keeps running. Only starts and jumps
// restore authored values and rewind the lanes.
func TestSceneLaunchAndDirectorDoNotRewindAutomation(t *testing.T) {
	for _, placement := range []bool{false, true} {
		cfg := rewindConfig(t, placement)
		cfg.Automation = []cmd.Command{
			{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.9), Tick: 600},
			{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.1), Tick: 1200},
		}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		var left, right [128]float32
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		for e.transport.Tick() < 700 {
			e.Render(left[:], right[:])
		}
		if e.voices[0].pan != .9 {
			t.Fatalf("%s: pan %g before the launch", scheduleKindName(placement), e.voices[0].pan)
		}
		commands := []cmd.Command{{Op: cmd.OpSetState, Track: 255, Index: 0}}
		if !placement {
			commands = append(commands, cmd.Command{Op: cmd.OpLaunchScene, Track: 255, Index: 0})
		}
		for _, c := range commands {
			e.Push(c)
		}
		e.Render(left[:], right[:])
		if e.voices[0].pan != .9 {
			t.Fatalf("%s: a launch or director change reset the automated value to %g", scheduleKindName(placement), e.voices[0].pan)
		}
		for e.transport.Tick() < 1300 {
			e.Render(left[:], right[:])
		}
		if e.voices[0].pan != .1 {
			t.Fatalf("%s: the lane stopped after a launch (%g)", scheduleKindName(placement), e.voices[0].pan)
		}
	}
}
