package pro

import (
	"math"
	"testing"
)

// fullPeak is the detector's filter without the silence skip.
func fullPeak(l *Limiter) float64 {
	index := int(l.frames & (limiterTaps - 1))
	center := int((l.frames - limiterHalf) & (limiterTaps - 1))
	peak := math.Max(math.Abs(l.ring[0][center]), math.Abs(l.ring[1][center]))
	start := (index + 1) & (limiterTaps - 1)
	var sums [2][limiterPhases - 1]float64
	for tap := 0; tap < limiterTaps; tap++ {
		for channel := range sums {
			x := l.ring[channel][start+tap]
			for phase := range sums[channel] {
				sums[channel][phase] += float64(l.coeff[tap][phase] * x)
			}
		}
	}
	for channel := range sums {
		for _, value := range sums[channel] {
			peak = math.Max(peak, math.Abs(value))
		}
	}
	return peak
}

// The detector skips its filter while the window holds only zeros; it must
// return the filter's exact result at every frame, through bursts, signed
// zeros, single impulses and silence.
func TestLimiterSilenceSkipMatchesTheFilter(t *testing.T) {
	l, err := NewLimiter(48000, DefaultLimiterParams())
	if err != nil {
		t.Fatal(err)
	}
	negativeZero := math.Copysign(0, -1)
	seed := uint32(1)
	noise := func() float64 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return float64(int32(seed)) / (1 << 31)
	}
	skipped := 0
	for frame := 0; frame < 20000; frame++ {
		var left, right float64
		switch {
		case frame < 300, frame >= 5000 && frame < 5100:
			left, right = noise(), noise()
		case frame == 2000:
			left = 1e-30
		case frame == 3000:
			right = negativeZero
			left = negativeZero
		case frame == 4000:
			right = 5e-324
		}
		got := l.detector(left, right)
		want := fullPeak(l)
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("frame %d: detector %v, filter %v", frame, got, want)
		}
		if l.nonzero == 0 {
			skipped++
		}
		l.frames++
	}
	if skipped < 10000 {
		t.Fatalf("the filter was skipped in only %d frames", skipped)
	}
	l.Reset()
	if l.nonzero != 0 {
		t.Fatal("Reset left nonzero samples counted")
	}
}
