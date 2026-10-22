package midi

import (
	"math"
	"testing"
	"time"
)

func TestObservationHostTimeRetainsAgeAndRejectsUnavailableClocks(t *testing.T) {
	const wall = int64(1_700_000_000_000_000_000)
	const host = int64(time.Hour)
	for _, tc := range []struct {
		observed, now, native, want int64
		valid                       bool
	}{
		{wall, wall, host, host, true},
		{wall, wall + int64(80*time.Millisecond), host, host - int64(80*time.Millisecond), true},
		{wall, wall + int64(250*time.Millisecond), host, host - int64(250*time.Millisecond), true},
		{wall, wall + int64(251*time.Millisecond), host, 0, false},
		{wall + 1, wall, host, 0, false},
		{0, wall, host, 0, false},
		{-1, wall, host, 0, false},
		{wall, wall, 0, 0, false},
		{wall, wall + 20, 10, 0, false},
		{math.MaxInt64 - 10, math.MaxInt64, math.MaxInt64, math.MaxInt64 - 10, true},
	} {
		got, valid := ObservationHostTime(tc.observed, tc.now, tc.native)
		if got != tc.want || valid != tc.valid {
			t.Fatalf("observation (%d,%d,%d) = %d,%v, want %d,%v", tc.observed, tc.now, tc.native, got, valid, tc.want, tc.valid)
		}
	}
}
