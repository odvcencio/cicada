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

// The reach of a hit's steps is the same number of windows at every sample rate
// the command reads, since a window is 2 ms long: 28 ms back from the window where
// the hit was found, and 6, 8 and 16 ms between the steps of one hit.
func TestStepReachIsTheSameNumberOfWindowsAtEveryRate(t *testing.T) {
	want := stepReach{back: 14, joined: 3, sharp: 4, sharper: 8, room: 15}
	for _, rate := range []int{8000, 11025, 16000, 22050, 32000, 44100, 48000, 88200, 96000, 176400, 192000} {
		if got := stepWindows(rate, max(1, rate/500)); got != want {
			t.Errorf("at %d Hz the reach is %+v, want %+v", rate, got, want)
		}
	}
}

// A hit begins with a step, a 2 ms window at least twice as loud as the 12 ms
// before it, and the search runs back from the window where the hit was found.
// The hit's beginning is the last step that reaches a twentieth of its loudest
// window, 26 dB down, with the steps that lead into it. A bump in a tail steps up
// too, but it is a weak step, under four times as loud as the 12 ms before it, and
// 30 dB or more below the hit. The steps that lead into a hit are close to it, or
// they are sharp, which a click or the first, soft stage of an attack is: four
// times as loud as the 12 ms before it and within 8 ms, or eight times as loud and
// within 16 ms, and at least a two-hundredth of the hit's loudest window. At 48 kHz
// the search reaches 14 windows back from the window where the hit was found.
func TestHitBeginIsTheFirstStepOfTheLastRunOfSteps(t *testing.T) {
	reach := stepWindows(48000, 96)
	if reach != (stepReach{back: 14, joined: 3, sharp: 4, sharper: 8, room: 15}) {
		t.Fatalf("the reach is %+v; the cases below are written for 14 windows back and steps 3, 4 and 8 windows apart", reach)
	}
	quiet := flat(-70, 20)
	// A tail that is not silent steps up in bumps of 8 dB, 2.5 times as loud as the
	// 12 ms before.
	tail := flat(-66, 20)
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
		// found after the peak, the bump is 35 dB below it, and 9 dB below a twentieth
		// of the window where the hit was found.
		{"a hit found early in its climb", energies(tail[:19], []float64{-58, -45, -35, -25, -20, -21}), 20, 20},
		{"a hit found after its loudest window", energies(flat(-53, 19), []float64{-45, -10, -14, -18, -22, -26}), 23, 20},

		// A bump in a tail steps up too, but it is a weak step, and 30 dB or more
		// below the hit.
		{"a bump just before the hit is not part of it", energies(tail[:19], []float64{-58, -30, -25, -26, -27}), 21, 20},
		{"a bump with a quiet window between", energies(tail[:18], []float64{-58, -66, -30, -25, -26, -27}), 22, 20},
		// The twentieth is 26 dB down: a weak bump 29 dB below the hit is not part of it.
		{"a bump 29 dB below the hit, two windows before", energies(flat(-62, 18), []float64{-54, -62, -30, -25, -26, -27}), 21, 20},
		{"a bump long before the hit", energies(tail[:10], []float64{-58}, flat(-66, 9), []float64{-30, -25, -26, -27}), 22, 20},
		// A louder bump, within 26 dB of the hit, is part of it when it is within 6 ms,
		// and not when it is further off.
		{"a loud bump three windows before the hit", energies(flat(-40, 17), []float64{-32, -40, -40, -22, -21, -20}), 21, 17},
		{"a loud bump four windows before the hit", energies(flat(-40, 16), []float64{-32, -40, -40, -40, -22, -21, -20}), 21, 20},

		// A click before the thump is loud enough to be heard. It belongs to the
		// hit when it comes close to it, and is a sound of its own when it does not.
		{"a click one window before the thump", energies(quiet[:19], []float64{-44, -30, -26, -25, -26}), 21, 19},
		{"a click with a quiet window before the thump", energies(quiet[:18], []float64{-44, -70, -30, -26, -25, -26}), 22, 18},
		{"a click three windows before the thump", energies(quiet[:17], []float64{-44, -70, -70, -30, -26, -25}), 22, 17},
		// Past 6 ms the click must be sharp. It comes out of quiet, so it is far
		// more than four times as loud as the 12 ms before it.
		{"a click four windows before the thump", energies(quiet[:16], []float64{-44, -70, -70, -70, -30, -26, -25}), 21, 16},
		{"a quiet click four windows before the thump", energies(quiet[:16], []float64{-55, -70, -70, -70, -30, -26, -25}), 21, 16},
		// And not when it is under a two-hundredth of the thump, 46 dB down.
		{"a click just over a two-hundredth of the thump", energies(flat(-85, 16), []float64{-60, -85, -85, -85, -20, -16, -15}), 21, 16},
		{"a click under a two-hundredth of the thump", energies(flat(-85, 16), []float64{-65, -85, -85, -85, -20, -16, -15}), 21, 20},
		// Eight times as loud reaches 16 ms.
		{"a click eight windows before the thump", energies(quiet[:12], []float64{-44}, flat(-70, 7), []float64{-30, -26, -25}), 21, 12},
		{"a click nine windows before the thump", energies(quiet[:11], []float64{-44}, flat(-70, 8), []float64{-30, -26, -25}), 21, 20},
		// A step five times as loud as the 12 ms before it is sharp, but not eight times.
		{"a step five times as loud, four windows before", energies(flat(-60, 16), []float64{-46, -60, -60, -60, -30, -26, -25}), 21, 16},
		{"a step five times as loud, six windows before", energies(flat(-60, 14), []float64{-46}, flat(-60, 5), []float64{-30, -26, -25}), 21, 20},
		{"a step seven times as loud, six windows before", energies(flat(-60, 14), []float64{-43}, flat(-60, 5), []float64{-30, -26, -25}), 21, 20},
		{"a step nine times as loud, six windows before", energies(flat(-60, 14), []float64{-41}, flat(-60, 5), []float64{-30, -26, -25}), 21, 14},

		// The first stage of an attack, softer than a twentieth of the hit but still
		// a two-hundredth of it, comes out of quiet as a sharp step.
		{"a soft first stage four windows long", energies(quiet[:15], flat(-45, 4), []float64{-10, -12, -14, -16, -18}), 20, 15},
		{"a soft first stage eight windows long", energies(quiet[:11], flat(-45, 8), []float64{-10, -12, -14, -16, -18}), 20, 11},
		{"a soft first stage nine windows long", energies(quiet[:10], flat(-45, 9), []float64{-10, -12, -14, -16, -18}), 20, 19},
		{"a first stage under a two-hundredth of the hit", energies(flat(-80, 15), flat(-60, 4), []float64{-10, -12, -14, -16, -18}), 20, 19},
		// A first stage that rises in small steps is a run of weak steps; only its
		// first window comes out of quiet.
		{"a soft first stage that rises", energies(quiet[:15], []float64{-46, -44, -42, -40}, []float64{-10, -12, -14, -16, -18}), 20, 15},

		// Each step is twice the one before: from -58 to -37 dBFS over four steps.
		{"steps that follow each other closely", energies(flat(-70, 12), []float64{-58, -70, -51, -70, -44, -70, -37, -34, -35, -34}), 19, 12},
		// Weak steps of 8 dB, each 3 windows after the one before, lead into the hit
		// and each 4 windows after it do not.
		{"weak steps three windows apart", energies(tail[:12], flat(-58, 3), flat(-50, 3), flat(-42, 3), flat(-34, 3)), 22, 12},
		{"weak steps four windows apart", energies(tail[:12], flat(-58, 4), flat(-50, 4), flat(-42, 4), []float64{-34, -33, -34}), 25, 24},
		// Nothing but silence comes before the first window that has any sound.
		{"after exact silence", energies(flat(silence, 20), []float64{-30, -25, -26, -27}), 21, 20},
		{"a recording that begins with the hit", energies([]float64{-30, -25, -26}), 1, 0},
		{"a hit that began further back than the search reaches", energies(quiet[:2], []float64{-30}, flat(-30.5, 24)), 25, -1},
		// A swell has no step, since each window is under twice the 12 ms before.
		{"a swell", energies(ramp(-70, -30, 40)), 38, -1},
		// A small step at the start of a swell does not make a beginning: a hit
		// begins at a step that reaches a twentieth of its loudest window, and the
		// rest of the swell has none.
		{"a swell that began with a bump", energies(flat(-70, 10), []float64{-60}, ramp(-58, -20, 10)), 20, -1},
		{"a swell that began with a sharp sound", energies(flat(-70, 10), []float64{-55}, ramp(-53, -20, 9)), 19, -1},
		{"a tail with no hit", energies(flat(-60, 40)), 30, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			begin, found := hitBegin(test.raw, test.found, reach, 0)
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

// A first stage or a click out of the room is a step out of quiet whatever the level
// of the room. Against the 12 ms before it, which are the room, it is only 8 dB over
// them, 2.5 times, in a soft take: too little for a sharp step, which is four or
// eight times as loud. It joins the hit, within 16 ms of the next step, when it is
// a step and the 30 ms before it are no louder than twice the room; a room of 0 is
// not known, and then nothing does.
func TestHitBeginJoinsAStepOutOfTheKnownRoom(t *testing.T) {
	reach := stepWindows(48000, 96)
	const roomDB = -60
	room := math.Pow(10, roomDB/20)
	at := func(over float64) float64 { return roomDB + over }
	for _, test := range []struct {
		name  string
		raw   []float64
		room  float64
		begin int
	}{
		{"a stage 8 dB over the room, 12 ms ahead", energies(flat(roomDB, 14), []float64{at(8)}, flat(roomDB, 5), []float64{-20, -18, -17}), room, 14},
		{"a stage 8 dB over the room, 16 ms ahead", energies(flat(roomDB, 12), []float64{at(8)}, flat(roomDB, 7), []float64{-20, -18, -17}), room, 12},
		{"a stage 8 dB over the room, 18 ms ahead", energies(flat(roomDB, 11), []float64{at(8)}, flat(roomDB, 8), []float64{-20, -18, -17}), room, 20},
		{"a stage 8 dB over a room that is not known", energies(flat(roomDB, 14), []float64{at(8)}, flat(roomDB, 5), []float64{-20, -18, -17}), 0, 20},
		{"a stage 14 dB over the room, 12 ms ahead", energies(flat(roomDB, 14), []float64{at(14)}, flat(roomDB, 5), []float64{-20, -18, -17}), room, 14},
		// The 12 ms before the stage are within twice the room, 6 dB over it, or not.
		{"a stage after 12 ms at 5 dB over the room", energies(flat(at(5), 14), []float64{at(5 + 8)}, flat(at(5), 5), []float64{-20, -18, -17}), room, 14},
		{"a stage after 12 ms at 7 dB over the room", energies(flat(at(7), 14), []float64{at(7 + 8)}, flat(at(7), 5), []float64{-20, -18, -17}), room, 20},
		// And the 30 ms before it: a bump in a tail that was 8 dB over the room 28 ms
		// ago is no step out of the room, and one 30 ms ago is.
		{"a stage 28 ms after a tail 8 dB over the room", energies(flat(at(8), 2), flat(roomDB, 14), []float64{at(8)}, flat(roomDB, 3), []float64{-20, -18, -17}), room, 20},
		{"a stage 30 ms after a tail 8 dB over the room", energies(flat(at(8), 1), flat(roomDB, 15), []float64{at(8)}, flat(roomDB, 3), []float64{-20, -18, -17}), room, 16},
		// A step has to be twice the 12 ms before it.
		{"a bump 5 dB over a quiet room", energies(flat(roomDB, 14), []float64{at(5)}, flat(roomDB, 5), []float64{-20, -18, -17}), room, 20},
		// It still has to reach a two-hundredth of the hit's loudest window, 46 dB down.
		{"a stage under a two-hundredth of the hit", energies(flat(-80, 14), []float64{-72}, flat(-80, 5), []float64{-20, -18, -17}), math.Pow(10, -80.0/20), 20},
		// A bump in a tail 30 dB over the room is no step out of the room.
		{"a bump in a tail", energies(flat(at(30), 14), []float64{at(38)}, flat(at(30), 5), []float64{-14, -12, -11}), room, 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			begin, found := hitBegin(test.raw, 21, reach, test.room)
			if !found || begin != test.begin {
				t.Errorf("the hit found at window 21 began at window %d (found %v), want window %d", begin, found, test.begin)
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

// A sharp step is more than four times as loud as the loudest of the 12 ms
// before it, and a sharper one more than eight times: a click out of quiet is far
// beyond both, and a bump in a beating tail is under three times.
func TestSharpStepsAreFourAndEightTimesAsLoudAsTheTwelveMillisecondsBefore(t *testing.T) {
	for _, test := range []struct {
		window float64 // the loudness of the window, after six windows as loud as 1 and one as loud as before
		before float64
		factor float64
		want   bool
	}{
		{3, 1, ringStepFactor, true},     // 3 is over twice 1
		{3, 1, ringSharpFactor, false},   // but not four times
		{4, 1, ringSharpFactor, false},   // four times is not more than four times
		{5, 1, ringSharpFactor, true},    // 5 is over four times 1
		{5, 1, ringSharperFactor, false}, // and not eight times
		{9, 1, ringSharperFactor, true},  // 9 is over eight times 1
		{9, 2, ringSharperFactor, false}, // the loudest of the six before counts, not the quietest: 9 is under eight times 2
	} {
		raw := append([]float64{test.before / 2, test.before, test.before / 3, test.before / 2, test.before / 2, test.before / 2}, test.window)
		if got := isStepBy(raw, 6, test.factor); got != test.want {
			t.Errorf("a window of %g after windows up to %g is a step by %g: %v, want %v", test.window, test.before, test.factor, got, test.want)
		}
	}
}
