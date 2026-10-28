package recording

import (
	"math"
	"slices"
	"testing"
)

// energies lists the energy of 2 ms windows from levels in dBFS. Each argument
// is a run of levels, so a quiet tail and the hit that follows are written as
// two runs. Negative infinity is exact silence.
func energies(runs ...[]float64) []float64 {
	var out []float64
	for _, level := range slices.Concat(runs...) {
		out = append(out, math.Pow(10, level/20))
	}
	return out
}

// flat is n windows at one level, in dBFS.
func flat(level float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = level
	}
	return out
}

// ramp is n levels in dBFS rising evenly from low to high.
func ramp(low, high float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = low + (high-low)*float64(i)/float64(n-1)
	}
	return out
}

// A hit begins with a step, a 2 ms window at least twice as loud as the 12 ms
// before it, and the search runs back from the window where the hit was found.
// The hit's beginning is the last step before that window, with the steps that
// lead into it. A bump in the tail also steps up, so a step counts only when it
// reaches a twentieth of the hit's loudest window, 26 dB down; and steps count
// as one hit only when no more than 2 windows lie between them. At 48 kHz the
// search reaches 14 windows back from the window where the hit was found.
func TestHitBeginIsTheFirstStepOfTheLastRunOfSteps(t *testing.T) {
	back, joined := stepWindows(48000, 96)
	if back != 14 || joined != 2 {
		t.Fatalf("the search reaches %d windows back and joins steps %d windows apart; the cases below are written for 14 and 2", back, joined)
	}
	quiet := flat(-70, 20)
	silence := math.Inf(-1)
	for _, test := range []struct {
		name  string
		raw   []float64
		found int // the window where the hit was found
		begin int // the window where it began, or -1 when it has no sharp beginning
	}{
		{"a hit out of a quiet tail", energies(quiet, []float64{-30, -25, -26, -27, -28}), 21, 20},
		{"a hit found a few windows after it began", energies(quiet, []float64{-30, -27, -26, -25, -26, -27}), 23, 20},
		// The twentieth is of the loudest window around the one where the hit was
		// found, not of that window itself. Found early in a climb, the bump is 38
		// dB below the peak but only 13 dB below the window where the hit was found;
		// found after the peak, the bump is 35 dB below it, and 3 dB above a twentieth
		// of the window where the hit was found.
		{"a hit found early in its climb", energies(quiet[:19], []float64{-58, -45, -35, -25, -20, -21}), 20, 20},
		{"a hit found after its loudest window", energies(quiet[:19], []float64{-45, -10, -14, -18, -22, -26}), 23, 20},
		// A bump in a tail steps up too, but it is 30 dB or more below the hit.
		{"a bump just before the hit is not part of it", energies(quiet[:19], []float64{-60, -30, -25, -26, -27}), 21, 20},
		{"a bump with a quiet window between", energies(quiet[:18], []float64{-60, -70, -30, -25, -26, -27}), 22, 20},
		{"a bump long before the hit", energies(quiet[:10], []float64{-62}, flat(-70, 9), []float64{-30, -25, -26, -27}), 22, 20},
		// A click before the thump is loud enough to be heard. It belongs to the
		// hit when it comes close to it, and is a sound of its own when it does not.
		{"a click one window before the thump", energies(quiet[:19], []float64{-44, -30, -26, -25, -26}), 21, 19},
		{"a click with a quiet window before the thump", energies(quiet[:18], []float64{-44, -70, -30, -26, -25, -26}), 22, 18},
		{"a click too long before the thump", energies(quiet[:17], []float64{-44, -70, -70, -30, -26, -25}), 22, 20},
		{"a loud sound long before the hit", energies(flat(-70, 12), []float64{-50}, flat(-70, 7), []float64{-36, -34, -35, -34}), 21, 20},
		// Each step is twice the one before: from -58 to -37 dBFS over four steps.
		{"steps that follow each other closely", energies(flat(-70, 12), []float64{-58, -70, -51, -70, -44, -70, -37, -34, -35, -34}), 19, 12},
		{"steps too far apart", energies(flat(-70, 12), []float64{-58, -70, -70, -70, -44, -70, -70, -70, -37, -34, -35, -34}), 21, 20},
		// Nothing but silence comes before the first window that has any sound.
		{"after exact silence", energies(flat(silence, 20), []float64{-30, -25, -26, -27}), 21, 20},
		{"a recording that begins with the hit", energies([]float64{-30, -25, -26}), 1, 0},
		{"a hit that began further back than the search reaches", energies(quiet[:2], []float64{-30}, flat(-30.5, 24)), 25, -1},
		// A swell has no step, since each window is under twice the 12 ms before.
		{"a swell", energies(ramp(-70, -30, 40)), 38, -1},
		{"a tail with no hit", energies(flat(-60, 40)), 30, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			begin, found := hitBegin(test.raw, test.found, back, joined)
			if test.begin < 0 {
				if found {
					t.Fatalf("the hit found at window %d began at window %d; it has no sharp beginning", test.found, begin)
				}
				return
			}
			if !found || begin != test.begin {
				t.Errorf("the hit found at window %d began at window %d (found %v), want window %d", test.found, begin, found, test.begin)
			}
		})
	}
}

// A step is a window at least twice as loud as the loudest of the six before it,
// which are the 12 ms before it. A window as loud as the ones before it is not
// one, and neither is a rise of less than 6 dB; and a louder window more than six
// windows back is out of reach.
func TestStepIsTwiceAsLoudAsTheLoudestOfTheTwelveMillisecondsBefore(t *testing.T) {
	raw := []float64{1, 1, 4, 1, 1, 1, 1, 1, 1, 2.1, 1.9}
	for k, want := range map[int]bool{
		0:  true,  // nothing comes before the first window
		1:  false, // as loud as the window before
		2:  true,  // four times as loud
		3:  false, // quieter than the 4 before it
		8:  false, // the 4 is still within the six windows before it
		9:  true,  // 2.1 is over twice 1: the 4, seven windows back, is out of reach
		10: false, // 1.9 is under twice the 2.1 before it
	} {
		if got := isStep(raw, k); got != want {
			t.Errorf("window %d of %v: step %v, want %v", k, raw, got, want)
		}
	}
	if isStep([]float64{1, 1, 4, 1, 1, 1, 1, 1, 7.9}, 8) {
		t.Error("window 8 is not twice the 4 at window 2, which is within six windows of it")
	}
}
