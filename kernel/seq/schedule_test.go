package seq

import (
	"reflect"
	"testing"
)

func testPattern(t *testing.T) Pattern {
	t.Helper()
	p := Pattern{Len: 16, SwingPermille: 80, GatePercent: 55, Seed: 4242}
	for i := 0; i < 16; i++ {
		step := Step{Note: uint8(45 + i%7), Gate: i%4 != 3, Ratchet: 1, Probability: 100, Velocity: 100}
		if i%5 == 0 {
			step.Ratchet = 3
		}
		if i%3 == 0 {
			step.Probability = 50
		}
		v, err := PackStep(step)
		if err != nil {
			t.Fatal(err)
		}
		p.Steps[i] = v
	}
	return p
}

func TestBlockSizeInvariance(t *testing.T) {
	c, _ := NewClock(48000, 138000)
	p := testPattern(t)
	end := c.SampleAtTick(64 * TicksPerBar)
	var reference []Event
	for _, block := range []int{32, 64, 128, 256, 480, 1024} {
		var events []Event
		var buf [128]Event
		for start := int64(0); start < end; start += int64(block) {
			frames := block
			if start+int64(frames) > end {
				frames = int(end - start)
			}
			n, overflow := EventsInBlock(&p, c, 0, 0, start, frames, buf[:])
			if overflow {
				t.Fatal("event buffer overflow")
			}
			for _, ev := range buf[:n] {
				ev.Offset = 0 // offset differs by block size; absolute sample must match
				events = append(events, ev)
			}
		}
		if reference == nil {
			reference = events
		} else if !reflect.DeepEqual(reference, events) {
			t.Fatalf("block size %d changed event log", block)
		}
	}
}

func TestRatchetTicks(t *testing.T) {
	want := []int64{0, 34, 68, 102, 136, 170, 204}
	for i, tick := range want {
		if got := RatchetTick(0, 240, 7, uint8(i)); got != tick {
			t.Fatalf("ratchet %d: got %d, want %d", i, got, tick)
		}
	}
}

func TestSwingPositions(t *testing.T) {
	for _, tc := range []struct {
		percent100 uint16
		ticks      int64
	}{
		{5000, 0}, {5400, 19}, {5800, 38}, {6667, 80}, {7500, 120},
	} {
		p, err := SwingFromPercent100(tc.percent100)
		if err != nil {
			t.Fatal(err)
		}
		if got := SwingDelayTicks(p); got != tc.ticks {
			t.Fatalf("swing %d: got %d, want %d", tc.percent100, got, tc.ticks)
		}
	}
}

func TestProbabilityDeterminism(t *testing.T) {
	count := 0
	for i := int64(0); i < 10000; i++ {
		a := ProbabilityHit(50, 4242, 1, 2, i, 3)
		b := ProbabilityHit(50, 4242, 1, 2, i, 3)
		if a != b {
			t.Fatal("same inputs changed decision")
		}
		if a {
			count++
		}
	}
	if count < 4800 || count > 5200 {
		t.Fatalf("50%% probability produced %d hits", count)
	}
}

func TestPackedStep(t *testing.T) {
	in := Step{Note: 57, Accent: true, Slide: true, Gate: true, Ratchet: 8, Probability: 73, Velocity: 127}
	v, err := PackStep(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnpackStep(v)
	if err != nil || out != in {
		t.Fatalf("step round trip: %+v, %v", out, err)
	}
}

func TestSlideFlagAppliesToNextNote(t *testing.T) {
	c, _ := NewClock(48000, 138000)
	p := Pattern{Len: 2, GatePercent: 55}
	p.Steps[0], _ = PackStep(Step{Note: 45, Gate: true, Slide: true, Ratchet: 1, Probability: 100})
	p.Steps[1], _ = PackStep(Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100})
	var events [8]Event
	n, overflow := EventsInBlock(&p, c, 0, 0, 0, int(c.SampleAtTick(2*TicksPerStep)), events[:])
	if overflow || n != 2 || events[0].Slide || !events[1].Slide {
		t.Fatalf("wrong slide events: n=%d overflow=%v events=%+v", n, overflow, events[:n])
	}
}

func TestEventsInBlockAllocs(t *testing.T) {
	c, _ := NewClock(48000, 138000)
	p := testPattern(t)
	var buf [128]Event
	n := testing.AllocsPerRun(1000, func() {
		EventsInBlock(&p, c, 0, 0, 0, 128, buf[:])
	})
	if n != 0 {
		t.Fatalf("scheduler allocated %v times per call", n)
	}
}

func TestHumanizeVelocityIsBoundedDeterministicAndOffByDefault(t *testing.T) {
	if HumanizeVelocity(90, 0, 7, 0, 0, 3, 2, 0, 0) != 90 {
		t.Fatal("amount 0 changed the velocity")
	}
	moved := false
	for iteration := int64(0); iteration < 200; iteration++ {
		got := HumanizeVelocity(90, 6, 7, 1, 0, iteration, 2, 0, 0)
		if got < 84 || got > 96 {
			t.Fatalf("iteration %d: %d is outside 90 +/- 6", iteration, got)
		}
		if got != 90 {
			moved = true
		}
		if got != HumanizeVelocity(90, 6, 7, 1, 0, iteration, 2, 0, 0) {
			t.Fatal("the offset is not deterministic")
		}
	}
	if !moved {
		t.Fatal("200 hits never moved")
	}
	for _, edge := range []uint8{1, 127} {
		for iteration := int64(0); iteration < 200; iteration++ {
			if got := HumanizeVelocity(edge, 32, 3, 0, 0, iteration, 0, 0, 0); got < 1 || got > 127 {
				t.Fatalf("velocity %d left 1..127: %d", edge, got)
			}
		}
	}
}

func TestHumanizeVelocityDiffersBetweenDrumLanesOnOneStep(t *testing.T) {
	// A drum lane step carries its lane as the note: kick, snare and hat at one
	// step must not all draw the same offset.
	offsets := map[int]bool{}
	for _, lane := range []uint8{0, 1, 2, 3, 4} {
		offsets[int(HumanizeVelocity(90, 10, 7, 1, 0, 3, 2, 0, lane))-90] = true
	}
	if len(offsets) < 2 {
		t.Fatalf("every lane drew the same offset: %v", offsets)
	}
	// Scheduling the lanes of one step through EventsInBlock shows the same.
	clock, _ := NewClock(48000, 120000)
	seen := map[uint8]bool{}
	for _, lane := range []uint8{0, 1, 2, 3, 4} {
		var p Pattern
		p.Len, p.GatePercent, p.Seed, p.Humanize = 4, 100, 7, 10
		packed, err := PackStep(Step{Note: lane, Gate: true, Velocity: 90, Ratchet: 1, Probability: 100})
		if err != nil {
			t.Fatal(err)
		}
		p.Steps[0] = packed
		var events [8]Event
		n, _ := EventsInBlock(&p, clock, 1, 0, 0, 48000, events[:])
		if n < 1 {
			t.Fatalf("lane %d scheduled no event", lane)
		}
		seen[events[0].Velocity] = true
	}
	if len(seen) < 2 {
		t.Fatalf("scheduled lanes share one velocity: %v", seen)
	}
}
