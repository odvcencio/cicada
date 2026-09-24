package seq

import (
	"reflect"
	"testing"
)

func gatePattern(t *testing.T, steps ...Step) Pattern {
	t.Helper()
	p := Pattern{Len: uint8(len(steps)), GatePercent: 50, Seed: 1}
	for i, step := range steps {
		packed, err := PackStep(step)
		if err != nil {
			t.Fatal(err)
		}
		p.Steps[i] = packed
	}
	return p
}

func note(number uint8) Step {
	return Step{Note: number, Gate: true, Ratchet: 1, Probability: 100}
}

func rest() Step { return Step{Ratchet: 1, Probability: 100} }

func collectGateEvents(t *testing.T, p Pattern, block int, bars int) []Event {
	t.Helper()
	clock, _ := NewClock(48_000, 120_000)
	end := clock.SampleAtTick(int64(bars) * TicksPerBar)
	var events []Event
	var buffer [128]Event
	for start := int64(0); start < end; start += int64(block) {
		frames := block
		if start+int64(frames) > end {
			frames = int(end - start)
		}
		count, overflow := EventsWithGatesInBlock(&p, clock, 0, 0, start, frames, buffer[:])
		if overflow {
			t.Fatal("event buffer overflow")
		}
		for _, event := range buffer[:count] {
			event.Offset = 0
			events = append(events, event)
		}
	}
	return events
}

func TestGateReleaseAndBlockInvariance(t *testing.T) {
	p := gatePattern(t, note(48), rest(), note(52), rest())
	reference := collectGateEvents(t, p, 64, 2)
	for _, block := range []int{32, 128, 256, 1024} {
		if got := collectGateEvents(t, p, block, 2); !reflect.DeepEqual(got, reference) {
			t.Fatalf("block %d changed gate events", block)
		}
	}
	if len(reference) < 2 || reference[0].Kind != NoteOn || reference[0].Tick != 0 || reference[1].Kind != NoteOff || reference[1].Tick != 120 || reference[0].NoteID != reference[1].NoteID {
		t.Fatalf("wrong first gate: %+v", reference[:min(len(reference), 2)])
	}
}

func TestTieHoldsThroughFollowingStep(t *testing.T) {
	tie := Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100}
	p := gatePattern(t, note(48), tie, rest(), rest())
	events := collectGateEvents(t, p, 1024, 1)
	if len(events) < 2 || events[0].Kind != NoteOn || events[1].Kind != NoteOff || events[1].Tick != 480 {
		t.Fatalf("tie release should be at end of tied step: %+v", events[:min(len(events), 2)])
	}
}

func TestSlideOverlapUsesExactSamples(t *testing.T) {
	first := note(48)
	first.Slide = true
	p := gatePattern(t, first, note(52), rest(), rest())
	events := collectGateEvents(t, p, 1024, 1)
	clock, _ := NewClock(48_000, 120_000)
	want := clock.SampleAtTick(TicksPerStep) + 240
	if len(events) < 3 || events[0].Kind != NoteOn || events[1].Kind != NoteOn || !events[1].Slide || events[2].Kind != NoteOff || events[2].Sample != want {
		t.Fatalf("slide overlap: %+v", events[:min(len(events), 3)])
	}
}

func TestSlideIntoMissedNoteReleasesNormally(t *testing.T) {
	first := note(48)
	first.Slide = true
	missed := note(52)
	missed.Probability = 0
	p := gatePattern(t, first, missed, rest(), rest())
	events := collectGateEvents(t, p, 1024, 1)
	if len(events) < 2 || events[0].Kind != NoteOn || events[1].Kind != NoteOff || events[1].Tick != 120 {
		t.Fatalf("missed slide target must release at normal gate: %+v", events[:min(len(events), 2)])
	}
}

func TestRatchetGateLength(t *testing.T) {
	step := note(48)
	step.Ratchet = 3
	p := gatePattern(t, step, rest())
	events := collectGateEvents(t, p, 1024, 1)
	if len(events) < 6 {
		t.Fatalf("missing ratchet events: %+v", events)
	}
	want := []struct {
		kind EventKind
		tick int64
	}{
		{NoteOn, 0}, {NoteOff, 40}, {NoteOn, 80}, {NoteOff, 120}, {NoteOn, 160}, {NoteOff, 200},
	}
	for i, expected := range want {
		if events[i].Kind != expected.kind || events[i].Tick != expected.tick {
			t.Fatalf("ratchet event %d: got %+v, want %+v", i, events[i], expected)
		}
	}
}

func TestGateSchedulerDoesNotAllocate(t *testing.T) {
	p := gatePattern(t, note(48), rest())
	clock, _ := NewClock(48_000, 120_000)
	var buffer [128]Event
	if count := testing.AllocsPerRun(100, func() {
		EventsWithGatesInBlock(&p, clock, 0, 0, 0, 1024, buffer[:])
	}); count != 0 {
		t.Fatalf("gate scheduler allocated %v times", count)
	}
}
