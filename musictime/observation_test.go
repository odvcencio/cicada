package musictime

import (
	"math"
	"testing"
)

func TestObservedTempoStepContinuityAndBounds(t *testing.T) {
	step := ObservedTempoStep{AfterNanos: 100_000_000, Tick: 3840, BPMMilli: 60000}
	for _, tc := range []struct {
		elapsed int64
		want    float64
	}{{-1, 0}, {50_000_000, 96}, {100_000_000, 192}, {200_000_000, 288}} {
		if got := ObservedAdvance(3648, 120000, tc.elapsed, step); got != tc.want {
			t.Fatalf("at %d: %g, want %g", tc.elapsed, got, tc.want)
		}
	}
	for _, bad := range []ObservedTempoStep{
		{}, {AfterNanos: -1, Tick: 3840, BPMMilli: 60000}, {AfterNanos: 351_000_000, Tick: 4320, BPMMilli: 60000},
		{AfterNanos: 100_000_000, Tick: 3840, BPMMilli: 19999}, {AfterNanos: 100_000_000, Tick: 3840, BPMMilli: 300001},
		{AfterNanos: 100_000_000, Tick: 3843, BPMMilli: 60000}, {AfterNanos: 100_000_000, Tick: 3647, BPMMilli: 60000},
		{AfterNanos: 100_000_000, Tick: math.MaxInt64, BPMMilli: 60000},
	} {
		if bad.Valid(3648, 120000) || ObservedAdvance(3648, 120000, 200_000_000, bad) != 384 {
			t.Fatalf("invalid metadata changed clock: %+v", bad)
		}
	}
	if !(ObservedTempoStep{Tick: 3840, BPMMilli: 60000}).Valid(3840, 120000) {
		t.Fatal("boundary at the observation was rejected")
	}
}
