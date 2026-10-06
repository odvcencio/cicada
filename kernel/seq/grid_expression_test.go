package seq

import "testing"

func TestTupletTieExpressionAtExactRestartTick(t *testing.T) {
	p := gatePattern(t, note(60), Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100}, note(64))
	p.StepTicks = 320
	p.Expression = new([64]Expression)
	p.Expression[1] = Expression{Set: true, PitchCents: 100, Timbre: .5}
	clock, _ := NewClock(48000, 120000)
	var dst [64]Event
	n, overflow := EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, 0, int(clock.SampleAtTick(1200)), dst[:])
	if overflow {
		t.Fatal("overflow")
	}
	count := 0
	for _, e := range dst[:n] {
		if e.Kind == NoteExpression {
			count++
			if e.Tick != 560 {
				t.Fatalf("expression tick %d; want 560", e.Tick)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expression count %d", count)
	}
	clock, _ = clock.Reanchor(700, 120000)
	n, _ = EventsWithGatesAtTickInBlock(&p, clock, 0, 0, 240, clock.AnchorSample, 128, dst[:])
	for _, e := range dst[:n] {
		if e.Kind == NoteExpression {
			t.Fatal("seek replayed a past expression")
		}
	}
}
