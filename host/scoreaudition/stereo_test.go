package scoreaudition

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMeasureStereo(t *testing.T) {
	cases := []struct {
		name        string
		left, right []float32
		corr        *float64
		mono        float64
	}{
		{"identical", []float32{1, -1}, []float32{1, -1}, f64(1), 0},
		{"opposite", []float32{1, -1}, []float32{-1, 1}, f64(-1), -120},
		{"uncorrelated", []float32{1, -1}, []float32{1, 1}, f64(0), -3.0102999566},
		{"left only", []float32{1, -1}, []float32{0, 0}, nil, -3.0102999566},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := measureStereo(stereo{tc.left, tc.right})
			if got == nil || math.Abs(got.MonoVsStereoDB-tc.mono) > 1e-8 {
				t.Fatalf("mono: %#v want %v", got, tc.mono)
			}
			if (got.ChannelCorrelation == nil) != (tc.corr == nil) {
				t.Fatalf("correlation: %#v", got.ChannelCorrelation)
			}
			if tc.corr != nil && math.Abs(*got.ChannelCorrelation-*tc.corr) > 1e-8 {
				t.Fatalf("correlation: %v", *got.ChannelCorrelation)
			}
			b, err := json.Marshal(got)
			if err != nil || strings.Contains(string(b), "NaN") {
				t.Fatalf("JSON %s: %v", b, err)
			}
		})
	}
	if got := measureStereo(stereo{[]float32{0}, []float32{0}}); got != nil {
		t.Fatalf("silence: %#v", got)
	}
}
func f64(v float64) *float64 { return &v }
