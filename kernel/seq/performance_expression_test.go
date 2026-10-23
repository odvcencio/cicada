package seq

import (
	"math"
	"reflect"
	"testing"
)

func TestPerformedExpressionKeepsControllerTimeAndOwnerAcrossBlocks(t *testing.T) {
	p := &Performance{LengthTicks: 960, Notes: []PerformedNote{
		{Tick: 17, EndTick: 700, Note: 60, Velocity: 90, Expressions: []PerformedExpression{
			{Tick: 0, Params: Expression{Set: true, Pressure: .2, Timbre: .5}},
			{Tick: 123, Params: Expression{Set: true, PitchCents: 123.5, Pressure: .7, Timbre: .9}},
			{Tick: 683, Params: Expression{Set: true, Pressure: 0, Timbre: .5}},
		}},
	}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		clock, _ := NewClock(rate, 123457)
		end := clock.SampleAtTick(1920)
		var want []Event
		for iteration := int64(0); iteration < 2; iteration++ {
			for i, tick := range []int64{17, 140, 700} {
				absolute := iteration*960 + tick
				sample := (absolute*60000*int64(rate) + 123457*960 - 1) / (123457 * 960)
				want = append(want, Event{Kind: NoteExpression, NoteID: iteration*512 + 1, Tick: absolute, Sample: sample, Note: 60, ExpressionIndex: uint16(i)})
			}
		}
		for _, block := range []int{1, 127, 1024} {
			var got []Event
			for frame := int64(0); frame < end; frame += int64(block) {
				var events [32]Event
				n, overflow := PerformanceEventsInBlock(p, clock, 0, 0, 0, frame, min(block, int(end-frame)), 0, events[:], true)
				if overflow {
					t.Fatal("unexpected expression overflow")
				}
				for _, event := range events[:n] {
					if event.Kind == NoteExpression {
						event.Offset = 0
						got = append(got, event)
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rate=%d block=%d: gesture timing or owner changed\ngot=%+v\nwant=%+v", rate, block, got, want)
			}
		}
	}
	clock, _ := NewClock(48000, 120000)
	var buffer [32]Event
	if n := testing.AllocsPerRun(1000, func() { PerformanceEventsInBlock(p, clock, 0, 0, 0, 0, 24000, 0, buffer[:], true) }); n != 0 {
		t.Fatal("controller playback allocated", n)
	}
}

func TestPerformedExpressionRejectsInvalidOrAmbiguousSamples(t *testing.T) {
	point := PerformedExpression{Tick: 1, Params: Expression{Set: true, Timbre: .5}}
	for _, change := range []func(*PerformedExpression){
		func(p *PerformedExpression) { p.Tick = -1 },
		func(p *PerformedExpression) { p.Tick = 121 },
		func(p *PerformedExpression) { p.Params.Pressure = float32(math.NaN()) },
		func(p *PerformedExpression) { p.Params.PitchCents = 9601 },
		func(p *PerformedExpression) { p.Params.Timbre = 2 },
		func(p *PerformedExpression) { p.Params.Set = false },
	} {
		invalid := point
		change(&invalid)
		p := &Performance{LengthTicks: 960, Notes: []PerformedNote{{Tick: 0, EndTick: 120, Note: 60, Velocity: 90, Expressions: []PerformedExpression{invalid}}}}
		if p.Validate() == nil {
			t.Fatal("invalid gesture admitted", invalid)
		}
	}
}

func TestPerformedPeakAdmissionMatchesIndependentCyclicWindows(t *testing.T) {
	p := &Performance{LengthTicks: 30, Notes: []PerformedNote{{Tick: 3, EndTick: 30, Note: 60, Velocity: 90, Expressions: []PerformedExpression{
		{Tick: 0, Params: Expression{Set: true, Timbre: .5}},
		{Tick: 16, Params: Expression{Set: true, Timbre: .5}},
		{Tick: 27, Params: Expression{Set: true, Timbre: .5}},
	}}}}
	for _, gates := range []bool{false, true} {
		for _, window := range []int64{0, 1, 11, 29, 30, 31, 60, 333} {
			best := 0
			for start := int64(30); start < 60; start++ {
				count := 0
				for iteration := int64(0); iteration < 20; iteration++ {
					positions := []int64{3, 19, 30}
					if gates {
						positions = append(positions, 3, 30)
					}
					for _, local := range positions {
						at := iteration*30 + local
						if at >= start && at <= start+window {
							count++
						}
					}
				}
				best = max(best, count)
			}
			if got := p.PeakEvents(window, gates); got != best {
				t.Fatalf("gates=%v window=%d: peak=%d want %d", gates, window, got, best)
			}
		}
	}
}
