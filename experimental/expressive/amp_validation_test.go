package expressive

import (
	"math"
	"testing"
)

func TestNewAmpUnsupportedRateUsesVoiceFallback(t *testing.T) {
	for _, rate := range []int{-48000, -1, 0, 7999, 192001, int(^uint(0) >> 1)} {
		a, reference := NewAmp(rate), NewAmp(48000)
		for i := 0; i < 4096; i++ {
			input := .4 * math.Sin(float64(i)*.13)
			got, want := a.Next(input, .7), reference.Next(input, .7)
			if !finite(got) || got != want {
				t.Fatalf("rate %d sample %d: got %g want fallback %g", rate, i, got, want)
			}
		}
	}
}
func TestNewAmpSupportedRateStability(t *testing.T) {
	for _, rate := range []int{8000, 44100, 48000, 96000, 192000} {
		a := NewAmp(rate)
		for i := 0; i < 8192; i++ {
			got := a.Next(.6*math.Sin(float64(i)*.11), 1)
			if !finite(got) || math.Abs(got) > 2 {
				t.Fatalf("rate %d sample %d unstable output %g", rate, i, got)
			}
		}
	}
}
