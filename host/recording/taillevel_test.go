package recording

import (
	"math"
	"slices"
	"testing"
)

// The level of a cut tail is the power mean of the energy over the 90 ms that end
// 4 ms before the cut. The energy is per 2 ms window, so those are 45 windows that
// end 2 windows before the last, and the windows of the hit's own start are never
// read.
func TestRingingTailLevelIsTheMeanOfNinetyMillisecondsEndingFourBeforeTheCut(t *testing.T) {
	const hop, skip, read = 96, 2, 45
	// A tail that beats every 6 windows, 12 ms: two windows in a lull at 1, four
	// at 5 in the lobe.
	var beating []float64
	for i := 0; i < 100; i++ {
		beating = append(beating, []float64{5, 5, 5, 5, 1, 1}[i%6])
	}
	mean := func(values ...float64) float64 {
		var sum float64
		for _, v := range values {
			sum += mul(v, v)
		}
		return math.Sqrt(sum / float64(len(values)))
	}
	t.Run("a lull at the cut does not hide the tail", func(t *testing.T) {
		// The cut falls at the end of window 99, a lull. The last six windows hold
		// a lull at 1, but the beat is read over 45 windows ending at window 97.
		got := tailLevel(beating, 0, 100*hop, hop, skip, read)
		if want := mean(beating[53:98]...); math.Abs(got-want) > 1e-12 {
			t.Errorf("the tail level is %g, want the power mean of windows 53 to 97, %g", got, want)
		}
		if lowest := slices.Min(beating[94:100]); got < 2*lowest {
			t.Errorf("the tail level %g is under twice the lowest of the last 12 ms, %g; the lull hides the beat", got, lowest)
		}
	})
	t.Run("the last 4 ms are left out", func(t *testing.T) {
		// A next hit that began late lifts the last two windows.
		energy := append(slices.Clone(beating[:98]), 400, 900)
		if got, want := tailLevel(energy, 0, 100*hop, hop, skip, read), tailLevel(beating, 0, 100*hop, hop, skip, read); got != want {
			t.Errorf("the tail level is %g with a rise in the last two windows, %g without", got, want)
		}
	})
	t.Run("a cut in the middle of a window", func(t *testing.T) {
		// The last frame is in window 69, so the reading ends at window 67.
		if got, want := tailLevel(beating, 0, 70*hop-10, hop, skip, read), mean(beating[23:68]...); math.Abs(got-want) > 1e-12 {
			t.Errorf("the tail level is %g, want the power mean of windows 23 to 67, %g", got, want)
		}
	})
	t.Run("a hit shorter than the reading is read from its first window", func(t *testing.T) {
		energy := []float64{50, 50, 50, 50, 50, 4, 2, 2, 2, 2}
		// The hit began at window 5; the 45 windows would reach back into the tail of
		// the hit before.
		if got, want := tailLevel(energy, 5, 10*hop, hop, skip, read), mean(4, 2, 2); math.Abs(got-want) > 1e-12 {
			t.Errorf("the tail level is %g, want the mean of the hit's own windows up to window 7, %g", got, want)
		}
		// A hit of a single window reads that window, even though the last two are left out.
		if got := tailLevel(energy, 9, 10*hop, hop, skip, read); got != 2 {
			t.Errorf("the tail level of a one-window hit is %g, want 2", got)
		}
	})
	t.Run("the end of the recording", func(t *testing.T) {
		// An end past the last window, as the last hit of a recording has, reads the
		// 45 windows that end two before the last.
		got := tailLevel(beating, 0, 100*hop+500, hop, skip, read)
		if want := mean(beating[53:98]...); math.Abs(got-want) > 1e-12 {
			t.Errorf("the tail level past the end is %g, want %g", got, want)
		}
	})
	t.Run("windows at other rates", func(t *testing.T) {
		for _, rate := range []int{44100, 48000, 96000} {
			if s, r := cutWindows(rate, max(1, rate/500)); s != 2 || r != 45 {
				t.Errorf("at %d Hz the cut level leaves out %d windows and reads %d, want 2 and 45", rate, s, r)
			}
		}
	})
}
