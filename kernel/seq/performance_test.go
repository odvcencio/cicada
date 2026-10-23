package seq

import (
	"reflect"
	"sort"
	"testing"
)

func TestPerformedGatesKeepTimingAcrossBlocksAndLoopBoundaries(t *testing.T) {
	performance := &Performance{LengthTicks: 960, Notes: []PerformedNote{
		{Tick: 0, EndTick: 960, Note: 60, Velocity: 82},
		{Tick: 17, EndTick: 901, Note: 64, Velocity: 105},
		{Tick: 31, EndTick: 70, Note: 67, Velocity: 47},
	}}
	if err := performance.Validate(); err != nil {
		t.Fatal(err)
	}
	pattern := Pattern{Len: 1, GatePercent: 55, Performance: performance}
	for _, rate := range []int{44100, 48000, 96000} {
		clock, _ := NewClock(rate, 123457)
		const restart = int64(19)
		end := clock.SampleAtTick(restart + 1920)
		var want []Event
		for iteration := int64(0); iteration < 2; iteration++ {
			for i, note := range performance.Notes {
				for kind, local := range []int64{note.Tick, note.EndTick} {
					tick := restart + iteration*960 + local
					// Independent integer mapping checks sub-step positions rather
					// than comparing the scheduler with a second scheduler call.
					sample := (tick*60000*int64(rate) + 123457*960 - 1) / (123457 * 960)
					if sample < end {
						want = append(want, Event{Kind: EventKind(kind), NoteID: iteration*512 + int64(i) + 1, Tick: tick, Sample: sample, Track: 2, Slot: 3, Note: note.Note, Velocity: note.Velocity})
					}
				}
			}
		}
		sort.Slice(want, func(i, j int) bool { return eventBefore(want[i], want[j]) })
		for _, block := range []int{1, 127, 1024} {
			var got []Event
			for frame := int64(0); frame < end; frame += int64(block) {
				var buffer [32]Event
				n, overflow := EventsWithGatesAtTickInBlock(&pattern, clock, 2, 3, restart, frame, min(block, int(end-frame)), buffer[:])
				if overflow {
					t.Fatal("unexpected performance overflow")
				}
				for _, event := range buffer[:n] {
					event.Offset = 0
					got = append(got, event)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rate=%d block=%d: performed gates changed\n got %+v\nwant %+v", rate, block, got, want)
			}
		}
	}
}

func TestPerformedGatesReportOverflowAndAllocateNothing(t *testing.T) {
	p := &Performance{LengthTicks: 960, Notes: []PerformedNote{{Tick: 0, EndTick: 120, Note: 60, Velocity: 90}, {Tick: 0, EndTick: 90, Note: 64, Velocity: 100}}}
	clock, _ := NewClock(48000, 120000)
	var small [1]Event
	if n, overflow := PerformanceEventsInBlock(p, clock, 0, 0, 0, 0, 4000, 0, small[:], true); n != 1 || !overflow {
		t.Fatal("bounded destination silently lost a chord", n, overflow)
	}
	var buffer [8]Event
	if allocations := testing.AllocsPerRun(1000, func() {
		PerformanceEventsInBlock(p, clock, 0, 0, 0, 0, 4000, 0, buffer[:], true)
	}); allocations != 0 {
		t.Fatal("performed gate scheduling allocated", allocations)
	}
}

func TestPerformanceRefusesUnrepresentableGates(t *testing.T) {
	valid := Performance{LengthTicks: 960, Notes: []PerformedNote{{Tick: 0, EndTick: 120, Note: 60, Velocity: 90}}}
	for _, note := range []PerformedNote{
		{Tick: -1, EndTick: 1, Note: 60, Velocity: 90},
		{Tick: 0, EndTick: 0, Note: 60, Velocity: 90},
		{Tick: 0, EndTick: 961, Note: 60, Velocity: 90},
		{Tick: 0, EndTick: 1, Note: 128, Velocity: 90},
		{Tick: 0, EndTick: 1, Note: 60, Velocity: 0},
	} {
		p := valid
		p.Notes = []PerformedNote{note}
		if p.Validate() == nil {
			t.Fatal("accepted invalid performed gate", note)
		}
	}
	valid.Notes = append(valid.Notes, PerformedNote{Tick: 119, EndTick: 240, Note: 60, Velocity: 80})
	if valid.Validate() == nil {
		t.Fatal("accepted overlapping gates sharing one pitch identity")
	}
	valid.Notes[1].Tick = 120
	if err := valid.Validate(); err != nil {
		t.Fatal("adjacent retrigger was refused", err)
	}
}
