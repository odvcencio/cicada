package seq

import (
	"reflect"
	"testing"
)

func chordPattern(t *testing.T) Pattern {
	t.Helper()
	p := Pattern{Len: 4, GatePercent: 55, Seed: 7}
	p.Steps[0], _ = PackStep(Step{Note: 62, Gate: true, Ratchet: 1, Probability: 70, Velocity: 100})
	p.Chords[0] = ChordStep{Notes: [4]uint8{62, 65, 69}, Count: 3}
	p.Steps[1], _ = PackStep(Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100})
	for i := 2; i < 4; i++ {
		p.Steps[i], _ = PackStep(Step{Ratchet: 1, Probability: 100})
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChordChanceTiesCohortAndBlockParity(t *testing.T) {
	p := chordPattern(t)
	p.Transpose = 12
	clock, _ := NewClock(48000, 120000)
	collect := func(block int) []Event {
		var out []Event
		var scratch [128]Event
		for at := int64(0); at < 192000; at += int64(block) {
			n, overflow := EventsWithGatesInBlock(&p, clock, 0, 0, at, min(block, int(192000-at)), scratch[:])
			if overflow {
				t.Fatal("overflow")
			}
			for _, event := range scratch[:n] {
				event.Offset = 0
				out = append(out, event)
			}
		}
		return out
	}
	reference := collect(4096)
	if len(reference) == 0 {
		t.Fatal("no probability cohort observed")
	}
	for _, size := range []int{1, 17, 128, 511} {
		if got := collect(size); !reflect.DeepEqual(reference, got) {
			t.Fatalf("event parity block%d", size)
		}
	}
	onsets := map[int64]Event{}
	for _, event := range reference {
		if event.NoteCount != 3 || event.Notes != [4]uint8{74, 77, 81} {
			t.Fatalf("partial chord: %+v", event)
		}
		if event.Kind == NoteOn {
			onsets[event.NoteID] = event
		} else {
			on := onsets[event.NoteID]
			if event.Tick-on.Tick != 480 {
				t.Fatalf("tie did not hold complete chord: %+v %+v", on, event)
			}
		}
	}
	p.Steps[0], _ = PackStep(Step{Note: 62, Gate: true, Ratchet: 1, Probability: 0})
	var dst [128]Event
	if n, _ := EventsWithGatesInBlock(&p, clock, 0, 0, 0, 192000, dst[:]); n != 0 {
		t.Fatal("zero-chance chord emitted partial events")
	}
}
func TestChordValidationBounds(t *testing.T) {
	p := chordPattern(t)
	for _, count := range []uint8{1, 5, 255} {
		q := p
		q.Chords[0].Count = count
		if q.Validate() == nil {
			t.Fatal("bad count accepted")
		}
	}
	for _, n := range []uint8{62, 128, 255} {
		q := p
		q.Chords[0].Notes[1] = n
		if q.Validate() == nil {
			t.Fatal("bad pitch accepted")
		}
	}
	q := p
	q.Chords[63] = p.Chords[0]
	if q.Validate() == nil {
		t.Fatal("out-of-length payload accepted")
	}
}
