package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func liveIdentityConfig(voices int) Config {
	cfg := expressionConfig()
	if voices == 4 {
		cfg.Track[0].Polyphony = 4
	} else {
		cfg.Track[0].Kind = VoiceGraphPoly
	}
	cfg.MaxVoices = voices
	return cfg
}

func TestLiveIdentityPolyNotesAndControllersStayIndependent(t *testing.T) {
	for _, voices := range []int{4, 8} {
		t.Run(string(rune('0'+voices)), func(t *testing.T) {
			e, err := New(liveIdentityConfig(voices))
			if err != nil {
				t.Fatal(err)
			}
			e.meterRate = 0
			var left, right [128]float32
			render := func() {
				e.Render(left[:], right[:])
				var m cmd.Message
				for e.Poll(&m) {
					if m.Kind == cmd.Fault {
						t.Fatal(m)
					}
				}
			}
			next := func() float32 {
				if e.voices[0].poly != nil {
					return e.voices[0].poly.Next()
				}
				v, _ := e.voices[0].legacyPoly.NextStereo()
				return v
			}
			// Equal pitches on separate identities must retain separate controls.
			if !e.PushBatch([]cmd.Command{expressionCommand(7, .2), expressionCommand(8, .3),
				{Op: cmd.OpNoteOn, Track: 0, Index: 7, Arg0: 60 | 100<<8, Arg1: cmd.LiveIdentity},
				{Op: cmd.OpNoteOn, Track: 0, Index: 8, Arg0: 60 | 100<<8, Arg1: cmd.LiveIdentity}}) {
				t.Fatal("owned notes rejected")
			}
			render()
			if got := next(); math.Abs(float64(got-.5)) > 1e-6 {
				t.Fatal("same-tick controls lost independence", got)
			}
			e.Push(expressionCommand(7, .8))
			render()
			if got := next(); math.Abs(float64(got-1.1)) > 1e-6 {
				t.Fatal("one controller changed another note", got)
			}
			e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 7, Arg1: cmd.LiveIdentity})
			render()
			for range 20 {
				render()
			}
			e.Push(expressionCommand(7, 1))
			render()
			if got := next(); math.Abs(float64(got-.3)) > 1e-6 {
				t.Fatal("old release or controller changed remaining note", got)
			}
			if allocations := testing.AllocsPerRun(1000, func() { e.Push(expressionCommand(8, .3)); render() }); allocations != 0 {
				t.Fatal("live poly callback allocated", allocations)
			}
			e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 65535, Arg1: cmd.LiveIdentity})
			render()
			for range 20 {
				render()
			}
			if got := next(); got != 0 {
				t.Fatal("live all-off kept a voice", got)
			}
		})
	}
}

func TestLiveIdentityCannotReleaseOrBendSequencedCohort(t *testing.T) {
	p := &seq.Performance{LengthTicks: 3840, Notes: []seq.PerformedNote{{Tick: 0, EndTick: 1920, Note: 60, Velocity: 90, Expressions: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Pressure: .2, Timbre: .5}}}}}}
	cfg := performanceConfig(liveIdentityConfig(4), p)
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	render := func() {
		e.Render(left[:], right[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatal(m)
			}
		}
	}
	render()
	e.PushBatch([]cmd.Command{{Op: cmd.OpNoteOn, Track: 0, Index: 1, Arg0: 60 | 100<<8, Arg1: cmd.LiveIdentity}, expressionCommand(1, .8)})
	render()
	if got := e.voices[0].poly.Next(); math.Abs(float64(got-1)) > 1e-6 {
		t.Fatal("live and score note IDs collided", got)
	}
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 65535, Arg1: cmd.LiveIdentity})
	render()
	for range 20 {
		render()
	}
	if got := e.voices[0].poly.Next(); got != .2 || e.patterns[0].performed.gates[60].id == 0 {
		t.Fatal("live all-off released the score", got)
	}
	if e.voices[0].poly.SetExpression(graph.Cohort{NoteID: 1, Generation: graph.LiveGeneration}, seq.Expression{Set: true, Pressure: 1, Timbre: .5}) {
		t.Fatal("released live cohort still mutable")
	}
}

func TestLiveIdentitySurvivesScoreReleaseAndStolenControls(t *testing.T) {
	for _, voices := range []int{4, 8} {
		e, err := New(liveIdentityConfig(voices))
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		var left, right [128]float32
		render := func() {
			e.Render(left[:], right[:])
			var m cmd.Message
			for e.Poll(&m) {
				if m.Kind == cmd.Fault {
					t.Fatal(m)
				}
			}
		}
		next := func() float32 {
			if e.voices[0].poly != nil {
				return e.voices[0].poly.Next()
			}
			v, _ := e.voices[0].legacyPoly.NextStereo()
			return v
		}
		for i := 1; i <= voices+1; i++ {
			e.PushBatch([]cmd.Command{{Op: cmd.OpNoteOn, Index: uint16(i), Arg0: 60 | 100<<8, Arg1: cmd.LiveIdentity}, expressionCommand(uint16(i), .1)})
			render()
		}
		e.Push(expressionCommand(1, 1)) // first slot has been stolen
		e.Push(cmd.Command{Op: cmd.OpNoteOff, Index: 1, Arg1: cmd.LiveIdentity})
		render()
		e.noteOff(0, 65535) // the same cleanup used for scene/track releases
		for range 30 {
			render()
		}
		if got := next(); math.Abs(float64(got)-float64(voices)*.1) > 1e-5 {
			t.Fatal("score or stale input changed live owners", voices, got)
		}
		e.Push(cmd.Command{Op: cmd.OpStop, Track: 255})
		for range 30 {
			render()
		}
		if got := next(); got != 0 {
			t.Fatal("transport stop left live owners sounding", got)
		}
	}
}
