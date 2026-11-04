package expression

import (
	"math"
	"testing"
)

func TestInstrumentVibratoDelayResetAndNoteExpression(t *testing.T) {
	var s State
	if err := s.ConfigureVibrato(VibratoConfig{DepthCents: 20, RateHz: 5, DelayMS: 4}, 1000); err != nil {
		t.Fatal(err)
	}
	s.Reset()
	for i := 0; i < 5; i++ {
		if got := s.NextCents(); got != 0 {
			t.Fatalf("delay sample %d = %g", i, got)
		}
	}
	if got := s.NextCents(); math.Abs(got-20*math.Sin(2*math.Pi*.005)) > 1e-9 {
		t.Fatalf("vibrato after delay = %g", got)
	}
	s.Reset()
	s.SetParams(Params{Set: true, Timbre: .5, VibratoDepthCents: 10, VibratoRateHz: 5}, 1000)
	for range 5 {
		s.NextCents()
	}
	want := 20*math.Sin(2*math.Pi*.005) + 10*math.Sin(2*math.Pi*.025)
	if got := s.NextCents(); math.Abs(got-want) > 1e-8 {
		t.Fatalf("instrument and per-note vibrato = %g, want %g", got, want)
	}
	if allocations := testing.AllocsPerRun(100, func() { s.NextRatio() }); allocations != 0 {
		t.Fatalf("vibrato allocated %g times", allocations)
	}
}
