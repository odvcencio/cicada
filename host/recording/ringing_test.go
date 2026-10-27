package recording

import (
	"math"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
)

// Drums and cymbals ring far longer than the 120 ms between taps. Ringing mode
// must find each hit once and keep its whole tail, where the tap detector
// reads the swells of a decay as new hits.
func TestRingingTakesKeepEachHitWhole(t *testing.T) {
	cases := []struct {
		drum   testwav.Drum
		levels []float64
		// length is the voice's ring in seconds, which spaces the hits in the
		// take, and keep is the least of it that each hit must retain.
		length, keep float64
	}{
		{testwav.Kick, []float64{.08, .09, .10, .085, .095}, .45, .42},
		{testwav.Kick, []float64{.78, .80, .82, .79, .81}, .45, .42},
		{testwav.Snare, []float64{.50, .55, .60, .52}, .35, .32},
		{testwav.ClosedHat, []float64{.35, .38, .36}, .12, .11},
		{testwav.OpenHat, []float64{.40, .42, .41, .43}, 1.4, 1.3},
		{testwav.Crash, []float64{.50, .52, .51}, 2.2, 2.1},
		{testwav.Ride, []float64{.30, .32, .31}, 2.6, 2.3},
	}
	for _, test := range cases {
		t.Run(string(test.drum), func(t *testing.T) {
			audio, err := DecodeWAV(testwav.Take(48000, test.drum, test.levels...))
			if err != nil {
				t.Fatal(err)
			}
			hits, err := Analyze([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != len(test.levels) {
				t.Fatalf("found %d hits, played %d", len(hits), len(test.levels))
			}
			slot := max(.75, test.length+.3)
			for i, h := range hits {
				played := .2 + float64(i)*slot
				if start := float64(h.Start) / 48000; math.Abs(start-played) > .01 {
					t.Errorf("hit %d starts at %.3f s, played at %.3f s", i+1, start, played)
				}
				if length := float64(h.End-h.Start) / 48000; length < test.keep {
					t.Errorf("hit %d keeps %.3f s of its ring, want at least %.2f s", i+1, length, test.keep)
				}
				if math.Abs(h.Peak-test.levels[i]) > .03*test.levels[i] {
					t.Errorf("hit %d peak %.3f, played at %.3f", i+1, h.Peak, test.levels[i])
				}
			}
		})
	}
}

// A hit that lands within half a second of the last one is part of its ring.
func TestRingingModeIgnoresOnsetsWithinHalfASecond(t *testing.T) {
	one, err := DecodeWAV(testwav.Take(48000, testwav.Snare, .5))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		seconds float64
		want    int
	}{{.3, 1}, {.6, 2}} {
		pcm := make([]float32, 48000*3)
		for _, at := range []int{9600, 9600 + int(test.seconds*48000)} {
			for i, x := range one.PCM[9600:] {
				if at+i < len(pcm) {
					pcm[at+i] += x
				}
			}
		}
		hits, err := Analyze([]Audio{{PCM: pcm, Rate: 48000}}, Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || len(hits) != test.want {
			t.Fatalf("hits %.1f s apart: found %d (%v), want %d", test.seconds, len(hits), err, test.want)
		}
	}
}
