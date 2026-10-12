package control

import (
	"math"
	"testing"
)

func TestVocalPhraseGainFollowsCoveredChosenPhrase(t *testing.T) {
	phrases := []VocalPhrase{{Take: "a", Start: 0, End: 100, GainDB: -6}, {Take: "a", Start: 100, End: 200, GainDB: 3}, {Take: "b", Start: 200, End: 300}}
	for _, tc := range []struct {
		take       string
		start, end int64
		want       float64
	}{
		{"a", 0, 100, -6}, {"a", 20, 80, -6}, {"a", 100, 200, 3}, {"a", 50, 150, 0}, {"b", 0, 100, 0}, {"b", 200, 300, 0}, {"a", 20, 20, 0},
	} {
		if got := VocalPhraseGain(phrases, tc.take, VocalRange{tc.start, tc.end}); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}

func TestVocalGainBoundsAndText(t *testing.T) {
	for _, db := range []float64{-24, 0, 18} {
		if !ValidVocalGain(db) {
			t.Errorf("valid %v rejected", db)
		}
	}
	for _, db := range []float64{-24.01, 18.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if ValidVocalGain(db) {
			t.Errorf("invalid %v accepted", db)
		}
	}
	for db, want := range map[float64]string{-6: "-6.0 dB", 3: "+3.0 dB", 0: "0.0 dB", -.01: "0.0 dB"} {
		if got := VocalGainText(db); got != want {
			t.Errorf("%v got %q", db, got)
		}
	}
}
