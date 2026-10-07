package seq

import (
	"reflect"
	"testing"
)

func TestTupletGridBlockInvarianceAndNoAllocs(t *testing.T) {
	p := gatePattern(t, note(48), note(52), note(55))
	p.StepTicks = 320
	want := collectGateEvents(t, p, 128, 2)
	for _, block := range []int{32, 64, 256, 1024} {
		if got := collectGateEvents(t, p, block, 2); !reflect.DeepEqual(got, want) {
			t.Fatalf("block %d changed triplets", block)
		}
	}
	for i, event := range want {
		if event.Tick != int64(i/2)*320+int64(i%2)*160 {
			t.Fatalf("event %d: %+v", i, event)
		}
	}
	clock, _ := NewClock(48000, 120000)
	var dst [64]Event
	if allocs := testing.AllocsPerRun(100, func() { EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, 6000, 128, dst[:]) }); allocs != 0 {
		t.Fatalf("%g allocations", allocs)
	}
}

func TestTupletRestartAtOffGridTick(t *testing.T) {
	p := gatePattern(t, note(48), note(52))
	p.StepTicks = 320
	clock, _ := NewClock(48000, 120000)
	var dst [64]Event
	n, overflow := EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, 0, 30000, dst[:])
	if overflow {
		t.Fatal("overflow")
	}
	for i, event := range dst[:n] {
		if event.Tick != 240+int64(i/2)*320+int64(i%2)*160 {
			t.Fatalf("event %d: %+v", i, event)
		}
	}
}

func TestTupletTiedExpressionAtOffGridTick(t *testing.T) {
	p := gatePattern(t, note(69), Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100}, rest(), rest())
	p.StepTicks = 320
	p.Expression = new([64]Expression)
	p.Expression[1] = Expression{Set: true, PitchCents: 200, Timbre: .5}
	clock, _ := NewClock(48000, 120000)
	var dst [64]Event
	n, overflow := EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, 0, 30000, dst[:])
	if overflow || n < 3 || dst[0].Kind != NoteOn || dst[1].Kind != NoteExpression || dst[1].Tick != 560 || dst[2].Kind != NoteOff {
		t.Fatalf("incorrect triplet expression timing: %+v (overflow=%v)", dst[:n], overflow)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, 0, 30000, dst[:])
	}); allocs != 0 {
		t.Fatalf("%g allocations", allocs)
	}
}

func TestTupletTiedExpressionDoesNotReplayBeforeSeek(t *testing.T) {
	p := gatePattern(t, note(69), Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100}, rest(), rest())
	p.StepTicks = 320
	p.Expression = new([64]Expression)
	p.Expression[1] = Expression{Set: true, PitchCents: 200}
	clock, _ := NewClock(48000, 120000)
	clock.AnchorTick = 900
	var dst [64]Event
	if n, overflow := EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, 0, 128, dst[:]); overflow || n != 0 {
		t.Fatalf("replayed events before seek: %+v (overflow=%v)", dst[:n], overflow)
	}
}
