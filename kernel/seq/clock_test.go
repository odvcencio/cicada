package seq

import (
	"math/rand"
	"testing"
)

func TestTickSampleRoundTrip(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, bpm := range []int64{20000, 138000, 300000} {
			c, err := NewClock(rate, bpm)
			if err != nil {
				t.Fatal(err)
			}
			rng := rand.New(rand.NewSource(42))
			for i := 0; i < 10000; i++ {
				tick := rng.Int63n(960 * 4 * 60 * 24) // up to 1,440 bars
				sample := c.SampleAtTick(tick)
				if got := c.TickAtSample(sample); got != tick {
					t.Fatalf("rate=%d bpm=%d tick=%d -> sample=%d -> %d", rate, bpm, tick, sample, got)
				}
				if sample > 0 && c.TickAtSample(sample-1) >= tick {
					t.Fatalf("sample %d is not the first sample for tick %d", sample, tick)
				}
			}
		}
	}
}

func TestTempoReanchor(t *testing.T) {
	c, _ := NewClock(48000, 138000)
	boundary := 17 * TicksPerBar
	before := c.SampleAtTick(boundary)
	c, err := c.Reanchor(boundary, 140000)
	if err != nil {
		t.Fatal(err)
	}
	if c.AnchorSample != before || c.SampleAtTick(boundary) != before || c.TickAtSample(before) != boundary {
		t.Fatal("tempo change shifted the bar boundary")
	}
	if c.TickAtSample(c.SampleAtTick(boundary+960)) != boundary+960 {
		t.Fatal("new tempo clock round trip failed")
	}
}

func TestNearestRoundingCounterexample(t *testing.T) {
	c, _ := NewClock(48000, 138000)
	// The spec's roundHalfUp would choose sample 43 for tick 2.
	if got := c.TickAtSample(43); got != 1 {
		t.Fatalf("counterexample changed: tick at sample 43 = %d", got)
	}
	if got := c.SampleAtTick(2); got != 44 {
		t.Fatalf("ceil conversion expected sample 44, got %d", got)
	}
}
