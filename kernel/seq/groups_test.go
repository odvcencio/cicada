package seq

import (
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
)

func sharedPerformance() *Performance {
	return &Performance{LengthTicks: 960, Groups: []ControlGroup{{Name: "touch", Points: []GroupPoint{{Tick: 100, Value: expression.Controls{PitchCents: 25, Pressure: .2, Timbre: .5}}, {Tick: 700, Value: expression.Controls{PitchCents: 200, Pressure: .8, Timbre: .9}}}}}, Notes: []PerformedNote{{Tick: 17, EndTick: 240, Note: 60, Velocity: 100, Group: 1}}}
}

func TestSharedGestureEventsContinueAfterReleaseAndResetEachLoop(t *testing.T) {
	p := sharedPerformance()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		clock, _ := NewClock(rate, 123457)
		var want []Event
		for iteration := int64(0); iteration < 2; iteration++ {
			for i, tick := range []int64{0, 100, 700} {
				e := Event{Kind: GroupExpression, Group: 1, NoteID: iteration * MaxPerformanceNotes, Tick: iteration*960 + tick, ExpressionIndex: uint16(max(0, i-1))}
				e.Sample = clock.SampleAtTick(e.Tick)
				if i == 0 {
					e.StepIndex = 1
				}
				want = append(want, e)
			}
		}
		for _, block := range []int{1, 127, 1024} {
			var got []Event
			end := clock.SampleAtTick(1920)
			for frame := int64(0); frame < end; frame += int64(block) {
				var events [32]Event
				n, over := PerformanceEventsInBlock(p, clock, 0, 0, 0, frame, min(block, int(end-frame)), 0, events[:], true)
				if over {
					t.Fatal("unexpected group overflow")
				}
				for _, e := range events[:n] {
					if e.Kind == GroupExpression {
						e.Offset = 0
						got = append(got, e)
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rate %d block %d group events changed: got %+v want %+v", rate, block, got, want)
			}
		}
		var buffer [32]Event
		if allocs := testing.AllocsPerRun(100, func() { PerformanceEventsInBlock(p, clock, 0, 0, 0, 0, 1024, 0, buffer[:], true) }); allocs != 0 {
			t.Fatal("shared event rendering allocated", allocs)
		}
	}
	if p.Groups[0].At(99).Pressure != 0 || p.Groups[0].At(100).PitchCents != 25 || p.Groups[0].At(701).Pressure != .8 {
		t.Fatal("shared lane chase failed")
	}
	if got := p.PeakEvents(960, false); got != 4 {
		t.Fatal("shared reset/sample admission missed cyclic boundary", got)
	}
}

func TestSharedGestureValidation(t *testing.T) {
	for _, mutate := range []func(*Performance){
		func(p *Performance) { p.Notes[0].Group = 2 },
		func(p *Performance) { p.Groups[0].Name = "bad name" },
		func(p *Performance) { p.Groups[0].Points[1].Tick = 960 },
		func(p *Performance) { p.Groups[0].Points[1].Tick = 100 },
		func(p *Performance) { p.Groups[0].Points[1].Value.PitchCents = 9601 },
		func(p *Performance) { p.Groups = append(p.Groups, p.Groups[0]) },
	} {
		p := sharedPerformance()
		mutate(p)
		if p.Validate() == nil {
			t.Fatal("invalid shared lane accepted")
		}
	}
}
