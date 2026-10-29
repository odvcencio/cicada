package recording

import (
	"fmt"
	"math"
	"reflect"
	"slices"
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
				if lead := leadIn(h.Start, played); lead < 0 || lead > 6 {
					t.Errorf("hit %d starts %.1f ms before its attack, played at %.3f s; want up to 6 ms", i+1, lead, played)
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

// spaced decodes a take made by testwav.Spaced.
func spaced(t *testing.T, d testwav.Drum, layout testwav.Layout, levels ...float64) Audio {
	t.Helper()
	audio, err := DecodeWAV(testwav.Spaced(48000, d, layout, levels...))
	if err != nil {
		t.Fatal(err)
	}
	return audio
}

// leadIn is how many milliseconds before the instant played, in seconds, a slice
// that starts at frame start begins at 48 kHz.
func leadIn(start int, played float64) float64 { return (played*48000 - float64(start)) / 48 }

// A cymbal that rings longer than the time between hits leaves a take with
// almost no quiet. The room's noise must come from the quietest stretch, not a
// percentile of the whole take, and each hit must keep its tail up to the next
// hit. Room tone of 0.5 s in 18 s, or 1 s in 27 s, is under 5% of the take, so
// a percentile would read the tails as the room: the first case then finds no
// hit at all, and the second cuts each tail short at a different length.
func TestRingingTailsThatFillTheTakeAreKeptToTheNextHit(t *testing.T) {
	levels := []float64{.55, .50, .60, .52, .58}
	for _, test := range []struct{ lead, spacing float64 }{{.5, 4}, {1, 6}, {3, 6}} {
		t.Run(fmt.Sprintf("%.1f s of room tone, hits %.0f s apart", test.lead, test.spacing), func(t *testing.T) {
			audio := spaced(t, testwav.LongCrash, testwav.Layout{Lead: test.lead, Spacing: test.spacing, After: 2, RoomDB: -70}, levels...)
			hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != len(levels) || len(reports) != 1 {
				t.Fatalf("found %d hits and %d reports, played %d hits", len(hits), len(reports), len(levels))
			}
			report := reports[0]
			if math.Abs(report.NoiseDB+70) > 3 || report.NoQuiet || math.Abs(report.QuietDB-report.NoiseDB-6) > 1 {
				t.Errorf("noise %.1f dBFS (room -70), no quiet stretch %v, quiet level %.1f dBFS", report.NoiseDB, report.NoQuiet, report.QuietDB)
			}
			for i, h := range hits {
				played := test.lead + float64(i)*test.spacing
				if lead := leadIn(h.Start, played); lead < 0 || lead > 6 {
					t.Errorf("hit %d starts %.1f ms before its attack, played at %.3f s; want up to 6 ms", i+1, lead, played)
				}
				if i < len(hits)-1 {
					// The tail is still 17 to 26 dB down when the next hit comes, and
					// the slice runs on to that hit.
					if length := float64(h.End-h.Start) / 48000; math.Abs(length-test.spacing) > .01 {
						t.Errorf("hit %d keeps %.3f s of its tail, want the %.1f s up to the next hit", i+1, length, test.spacing)
					}
				} else if h.End != len(audio.PCM) {
					t.Errorf("the last hit ends at frame %d, not at the end of the take, frame %d", h.End, len(audio.PCM))
				}
				// Every tail was cut while it still rang, well above the quiet
				// level and below where the hit started.
				if !h.Cut || h.CutDB < report.QuietDB+10 || h.CutDB > h.LoudnessDB-5 {
					t.Errorf("hit %d: cut %v at %.1f dBFS; its first 40 ms is at %.1f dBFS and a finished tail is below %.1f dBFS", i+1, h.Cut, h.CutDB, h.LoudnessDB, report.QuietDB)
				}
			}
		})
	}
}

// Recorded with no quiet at all, a take gives the command no room to measure.
// It must still find its hits, say that it found no quiet, and not let a high
// floor hide every hit.
func TestRingingTakeWithNoQuietStretchStillFindsItsHits(t *testing.T) {
	levels := []float64{.55, .50, .60, .52, .58}
	audio := spaced(t, testwav.LongCrash, testwav.Layout{Spacing: 4, After: 1, RoomDB: -80}, levels...)
	hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(levels) {
		t.Fatalf("found %d hits, played %d", len(hits), len(levels))
	}
	report := reports[0]
	// The quietest 200 ms is a tail 17 dB down, far from the room at -80 dBFS.
	if !report.NoQuiet || report.LoudestDB-report.NoiseDB > 26 {
		t.Errorf("no quiet stretch %v; the quietest 200 ms is %.1f dB below the loudest sound", report.NoQuiet, report.LoudestDB-report.NoiseDB)
	}
	// With no floor to trust, tails end 20 dB below the loudest sound.
	if got := report.LoudestDB - report.QuietDB; math.Abs(got-20) > 1 {
		t.Errorf("tails end %.1f dB below the loudest sound, want 20", got)
	}
	for i, h := range hits {
		if played := float64(i) * 4; leadIn(h.Start, played) < 0 || leadIn(h.Start, played) > 6 {
			t.Errorf("hit %d starts %.1f ms before its attack, played at %.1f s; want up to 6 ms", i+1, leadIn(h.Start, played), played)
		}
	}
}

// Recording with a room tone puts the noise floor where it is: the same hits
// with and without a quiet stretch must read differently.
func TestRingingTakesWithRoomToneAreNotFlaggedAsQuietless(t *testing.T) {
	audio := spaced(t, testwav.LongCrash, testwav.Layout{Lead: 1, Spacing: 14, After: 6, RoomDB: -80}, .55, .5)
	_, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil || reports[0].NoQuiet {
		t.Fatalf("room tone flagged as having no quiet stretch: %+v, %v", reports[0], err)
	}
}

// A soft take in a quiet room has hits only 20 to 25 dB above the room's noise,
// and the quietest 200 ms is then near the loudest sound. It still opens with
// room tone, so that stretch is the room and not a ring: the take must not be
// reported as having no quiet stretch, and its noise is read as measured.
func TestRingingSoftTakeThatOpensWithRoomToneIsNotFlaggedAsQuietless(t *testing.T) {
	audio := spaced(t, testwav.LongCrash, testwav.Layout{Lead: 3, Spacing: 8, After: 2, RoomDB: -65}, .012, .010, .011)
	hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil || len(hits) != 3 {
		t.Fatalf("found %d hits, played 3 (%v)", len(hits), err)
	}
	report := reports[0]
	if report.LoudestDB-report.NoiseDB > 26 || report.LoudestDB-report.NoiseDB < 15 {
		t.Fatalf("the test's room is %.1f dB below the loudest sound, but the case needs 15 to 26 dB", report.LoudestDB-report.NoiseDB)
	}
	if report.NoQuiet || math.Abs(report.NoiseDB+65) > 3 || math.Abs(report.QuietDB-report.NoiseDB-6) > 1 {
		t.Errorf("no quiet stretch %v, noise %.1f dBFS (room -65), quiet level %.1f dBFS", report.NoQuiet, report.NoiseDB, report.QuietDB)
	}
}

// The room's noise can only be read from the take's own opening when that
// holds 200 ms of room tone. The report says whether it does, for takes whose
// quietest stretch lies far below the loudest sound as well as for those whose
// does not.
func TestRingingReportsWhetherTheTakeOpensWithRoomTone(t *testing.T) {
	levels := []float64{.55, .50, .60}
	for _, test := range []struct {
		name                 string
		lead, spacing, after float64
		none                 bool // want NoRoomTone
		quiet                bool // want NoQuiet
	}{
		{"hit at the very start", 0, 6, 8, true, false},
		{"100 ms of room tone", .1, 6, 8, true, false},
		{"150 ms of room tone", .15, 6, 16, true, false},
		{"250 ms of room tone", .25, 6, 8, false, false},
		{"3 s of room tone", 3, 6, 8, false, false},
		{"no room tone, and tails that fill the take", 0, 4, 1, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			audio := spaced(t, testwav.LongCrash, testwav.Layout{Lead: test.lead, Spacing: test.spacing, After: test.after, RoomDB: -70}, levels...)
			_, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := reports[0]; got.NoRoomTone != test.none || got.NoQuiet != test.quiet {
				t.Errorf("no room tone %v, no quiet stretch %v; want %v and %v (quietest 200 ms at %.1f dBFS, loudest sound at %.1f dBFS)", got.NoRoomTone, got.NoQuiet, test.none, test.quiet, got.NoiseDB, got.LoudestDB)
			}
		})
	}
	// The tap detector reads no room tone, so it reports none missing.
	audio := spaced(t, testwav.Kick, testwav.Layout{Spacing: 1, After: 1}, .5, .6)
	if _, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1}); err != nil || reports[0].NoRoomTone {
		t.Errorf("the tap detector reported no room tone: %+v, %v", reports, err)
	}
}

// Hits only 13 dB above a room of -50 dBFS, with 3 s of room tone first: the
// room is the floor, tails finish 6 dB above it, and the room is never taken
// for a tail that was cut off.
func TestRingingLoudRoomWithRoomToneIsReadAsTheRoom(t *testing.T) {
	audio := spaced(t, testwav.Snare, testwav.Layout{Lead: 3, Spacing: 1.2, After: 2, RoomDB: -50}, .03, .033, .027, .031, .03)
	hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 38, Layers: 1, Ringing: true})
	if err != nil || len(hits) != 5 {
		t.Fatalf("found %d hits, played 5 (%v)", len(hits), err)
	}
	report := reports[0]
	if report.NoRoomTone || !report.NoQuiet || math.Abs(report.NoiseDB+50) > 1 {
		t.Errorf("no room tone %v, no quiet stretch %v, noise %.1f dBFS (room -50)", report.NoRoomTone, report.NoQuiet, report.NoiseDB)
	}
	// A tail has finished when it is 6 dB above the room, and not before.
	if math.Abs(report.QuietDB-report.NoiseDB-6) > 1 {
		t.Errorf("tails finish at %.1f dBFS, %.1f dB above the room; want 6 dB", report.QuietDB, report.QuietDB-report.NoiseDB)
	}
	for i, h := range hits {
		if h.Cut {
			t.Errorf("hit %d was reported as cut off at %.1f dBFS; that is the room, at %.1f dBFS", i+1, h.CutDB, report.NoiseDB)
		}
	}
}

// A cut-off tail fades out over about 5 ms. The first hit here is noise held at
// -24 dBFS until the second hit, which attacks at once, cuts it off.
func TestRingingCutHitFadesOutOverFiveMilliseconds(t *testing.T) {
	const rate = 48000
	state := uint32(1)
	noise := func() float64 {
		state = state*1664525 + 1013904223
		return float64(int32(state)) / (1 << 31)
	}
	pcm := make([]float32, rate*5)
	for i := range pcm {
		at := float64(i) / rate
		x := 1e-4 * noise()
		if at >= .5 && at < 2 {
			x += .1 * noise()
		}
		if at >= 2 {
			x += .8 * noise() * math.Exp(-(at-2)/.1)
		}
		pcm[i] = float32(x)
	}
	hits, _, err := AnalyzeTakes([]Audio{{PCM: pcm, Rate: rate}}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil || len(hits) != 2 || !hits[0].Cut {
		t.Fatalf("hits %d, error %v", len(hits), err)
	}
	held := hits[0].PCM
	// ms is the RMS of the stretch from far to near milliseconds before the end.
	ms := func(near, far float64) float64 {
		var sum float64
		a, b := len(held)-int(far*rate/1000), len(held)-int(near*rate/1000)
		for _, x := range held[a:b] {
			sum += float64(x) * float64(x)
		}
		return math.Sqrt(sum / float64(b-a))
	}
	steady := ms(10, 15)
	if before := ms(5.5, 7); math.Abs(before/steady-1) > .25 {
		t.Errorf("the tail 5.5 to 7 ms before the cut is at %.2f of its level 10 to 15 ms before, so the fade starts too early", before/steady)
	}
	// A linear fade over 5 ms leaves 0.23 of the level in the last 2 ms.
	if last := ms(0, 2); last > .4*steady {
		t.Errorf("the last 2 ms are at %.2f of the tail's level, so the cut is not faded over 5 ms", last/steady)
	}
	if held[len(held)-1] != 0 {
		t.Errorf("the last sample is %g, not 0", held[len(held)-1])
	}
}

// A tail that the next hit cuts just above the level where tails finish is not
// worth a warning: the level is a rule of thumb, and the message would say
// "cut at -64 dBFS, where a finished tail is below -64 dBFS". The hit is still
// faded out over 5 ms, but only a tail more than 6 dB above that level is
// reported as cut.
func TestRingingReportsACutOnlyWellAboveTheFinishedLevel(t *testing.T) {
	const rate = 48000
	for _, test := range []struct {
		name string
		tau  float64 // the tail's time constant: 1.5 s later it is 1.3 or 6.5 times the finished level
		cut  bool
	}{{"just above the finished level", .2427, false}, {"well above the finished level", .329, true}} {
		t.Run(test.name, func(t *testing.T) {
			state := uint32(7)
			noise := func() float64 {
				state = state*1664525 + 1013904223
				return float64(int32(state)) / (1 << 31)
			}
			pcm := make([]float32, rate*5)
			for i := range pcm {
				at := float64(i) / rate
				x := 1e-4 * noise()
				if at >= .5 && at < 2 {
					x += .5 * noise() * math.Exp(-(at-.5)/test.tau)
				}
				if at >= 2 {
					x += .8 * noise() * math.Exp(-(at-2)/.1)
				}
				pcm[i] = float32(x)
			}
			hits, reports, err := AnalyzeTakes([]Audio{{PCM: pcm, Rate: rate}}, Options{Root: 49, Layers: 1, Ringing: true})
			if err != nil || len(hits) != 2 {
				t.Fatalf("hits %d, error %v", len(hits), err)
			}
			// The tail's level in the 10 ms before the cut, measured in the source.
			var sum float64
			for _, x := range pcm[2*rate-rate/100-rate*3/1000 : 2*rate-rate*3/1000] {
				sum += float64(x) * float64(x)
			}
			above := dB(math.Sqrt(sum/(rate/100))) - reports[0].QuietDB
			if (test.cut && above < 12) || (!test.cut && (above < 0 || above > 5)) {
				t.Fatalf("the test's tail is %.1f dB above the finished level of %.1f dBFS at the cut", above, reports[0].QuietDB)
			}
			if hits[0].Cut != test.cut {
				t.Errorf("a tail %.1f dB above the finished level: cut %v at %.1f dBFS, want %v", above, hits[0].Cut, hits[0].CutDB, test.cut)
			}
			if test.cut && math.Abs(hits[0].CutDB-reports[0].QuietDB-above) > 3 {
				t.Errorf("the cut is reported at %.1f dBFS, %.1f dB above the finished level; it was %.1f dB above", hits[0].CutDB, hits[0].CutDB-reports[0].QuietDB, above)
			}
			// Either way the hit was cut off, so it fades out over 5 ms.
			if last := hits[0].PCM[len(hits[0].PCM)-1]; last != 0 {
				t.Errorf("the last sample is %g, not 0", last)
			}
			held := hits[0].PCM
			rms := func(from, to int) float64 {
				var sum float64
				for _, x := range held[len(held)-from : len(held)-to] {
					sum += float64(x) * float64(x)
				}
				return math.Sqrt(sum / float64(from-to))
			}
			if before, last := rms(rate*15/1000, rate*10/1000), rms(rate*2/1000, 0); last > .4*before {
				t.Errorf("the last 2 ms are at %.2f of the tail's level 10 to 15 ms before, so the cut is not faded over 5 ms", last/before)
			}
		})
	}
}

// Attacks that come within half a second of a hit cannot be separate hits, but
// they must not vanish unseen: every attack is a hit or a counted merge.
func TestRingingCountsAttacksThatMergedIntoAnEarlierHit(t *testing.T) {
	const played = 6
	for _, drum := range []testwav.Drum{testwav.Kick, testwav.Snare, testwav.ClosedHat} {
		for _, test := range []struct {
			spacing float64
			merged  bool
		}{{.25, true}, {.30, true}, {.40, true}, {.45, true}, {.55, false}, {.75, false}} {
			t.Run(fmt.Sprintf("%s %.2f s apart", drum, test.spacing), func(t *testing.T) {
				audio := spaced(t, drum, testwav.Layout{Lead: .3, Spacing: test.spacing, After: 1.5}, .5, .5, .5, .5, .5, .5)
				hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
				if err != nil {
					t.Fatal(err)
				}
				if got := len(hits) + reports[0].Merged; got != played || test.merged != (reports[0].Merged > 0) {
					t.Errorf("%d hits and %d merged attacks for %d played; merged expected: %v", len(hits), reports[0].Merged, played, test.merged)
				}
			})
		}
	}
}

// A tom whose second mode sits 12 Hz above the first beats into deep nulls.
// The energy climbs out of each null in a few milliseconds, which is not a new
// hit: it never rises above the swell before it.
func TestRingingBeatingTailIsNotANewHit(t *testing.T) {
	levels := []float64{.70, .85, .90, .75, .80, .90}
	audio, err := DecodeWAV(testwav.Take(48000, testwav.BeatingTom, levels...))
	if err != nil {
		t.Fatal(err)
	}
	hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 41, Layers: 1, Ringing: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(levels) || reports[0].Merged != 0 {
		t.Fatalf("found %d hits and %d merged attacks, played %d hits", len(hits), reports[0].Merged, len(levels))
	}
	for i, h := range hits {
		if lead := leadIn(h.Start, .2+float64(i)*2); lead < 0 || lead > 6 {
			t.Errorf("hit %d starts %.1f ms before its attack, played at %.3f s; want up to 6 ms", i+1, lead, .2+float64(i)*2)
		}
		if h.Cut {
			t.Errorf("hit %d was cut at %.1f dBFS; the tom rings out before the next hit", i+1, h.CutDB)
		}
	}
}

// A struck cymbal takes tens of milliseconds to reach full level. Each step of
// that swell is less than the whole swell before it, so a new hit cannot be
// asked to beat the energy of the last few milliseconds.
func TestRingingFindsCymbalHitsWithSlowAttacks(t *testing.T) {
	for _, spacing := range []float64{4, 8, 14} {
		audio := spaced(t, testwav.LongCrash, testwav.Layout{Lead: 1, Spacing: spacing, After: 2, RoomDB: -80}, .5, .52, .51, .5)
		hits, _, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
		if err != nil || len(hits) != 4 {
			t.Fatalf("hits %.0f s apart: found %d (%v), played 4", spacing, len(hits), err)
		}
		for i, h := range hits {
			// A hit that starts at the beginning of its swell is sliced from there.
			if lead := leadIn(h.Start, 1+float64(i)*spacing); lead < 0 || lead > 6 {
				t.Errorf("hits %.0f s apart: hit %d starts %.1f ms before its attack, played at %.3f s; want up to 6 ms", spacing, i+1, lead, 1+float64(i)*spacing)
			}
		}
	}
}

func TestAnalyzeTakesReportsEachInputAndAnalyzeKeepsItsHits(t *testing.T) {
	a, b := spaced(t, testwav.Snare, testwav.Layout{Lead: .3, Spacing: 1, After: 1}, .5, .6), spaced(t, testwav.Kick, testwav.Layout{Lead: .3, Spacing: 1, After: 1}, .7, .8, .75)
	for _, options := range []Options{{Root: 36, Layers: 1, Ringing: true}, {Root: 36, Layers: 1}} {
		hits, reports, err := AnalyzeTakes([]Audio{a, b}, options)
		if err != nil || len(reports) != 2 {
			t.Fatalf("ringing %v: %d reports, %v", options.Ringing, len(reports), err)
		}
		plain, err := Analyze([]Audio{a, b}, options)
		if err != nil || !reflect.DeepEqual(plain, hits) {
			t.Fatalf("ringing %v: Analyze and AnalyzeTakes found different hits (%v)", options.Ringing, err)
		}
		for _, h := range hits {
			if !options.Ringing && (h.Cut || h.CutDB != 0) {
				t.Errorf("the tap detector marked a hit as cut: %+v", h)
			}
		}
		if !options.Ringing && (reports[0].Merged != 0 || reports[1].Merged != 0) {
			t.Errorf("the tap detector counted merged attacks: %+v", reports)
		}
	}
}

// A louder strike within 120 ms of a hit is part of that hit, as in the tap
// detector, so it is not counted as an attack that merged. A strike 200 ms
// later is.
func TestRingingDoesNotCountASwellAsAMergedAttack(t *testing.T) {
	for _, test := range []struct {
		spacing float64
		merged  int
	}{{.08, 0}, {.20, 1}} {
		audio := spaced(t, testwav.Kick, testwav.Layout{Lead: .3, Spacing: test.spacing, After: 1.5}, .2, .8)
		hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || len(hits) != 1 || reports[0].Merged != test.merged {
			t.Errorf("strikes %.2f s apart: %d hits and %d merged attacks (%v); want 1 and %d", test.spacing, len(hits), reports[0].Merged, err, test.merged)
		}
	}
}

// beatingTom renders one hit of a tom: a decaying 82 Hz sine, a second decaying
// sine beat Hz above it at ratio times its level, a quieter overtone, and a
// burst of noise for the stick. Its peak is 1.
func beatingTom(seed uint64, seconds, beat, ratio, tau float64) []float64 {
	const rate = 48000
	state := seed
	noise := func() float64 { // splitmix64, as testwav uses
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		return float64(float64(z>>11)/float64(1<<53)*2) - 1
	}
	phases := [3]float64{(noise() + 1) * math.Pi, (noise() + 1) * math.Pi, (noise() + 1) * math.Pi}
	hit := make([]float64, int(seconds*rate))
	peak := 0.0
	for i := range hit {
		t := float64(i) / rate
		// Every product is rounded before it is added, as mul does for the analysis,
		// so the recording is the same on a processor that fuses x*y+z.
		turns := func(hz float64) float64 { return float64(2 * math.Pi * hz * t) }
		modes := math.Cos(turns(82)+phases[0]) + float64(ratio*math.Cos(turns(82+beat)+phases[1]))
		body := float64(math.Exp(-t/tau) * modes)
		overtone := float64(.4 * math.Exp(-t/(tau/2)) * math.Cos(turns(130)+phases[2]))
		stick := float64(.3 * noise() * math.Exp(-t/.004))
		hit[i] = body + overtone + stick
		peak = max(peak, math.Abs(hit[i]))
	}
	for i := range hit {
		hit[i] /= peak
	}
	return hit
}

// beatingTake places hits of beatingTom gap seconds apart, the first after lead
// seconds, over a room at -70 dBFS.
func beatingTake(lead, gap, beat, ratio, tau float64, levels ...float64) Audio {
	const rate = 48000
	pcm := make([]float64, int((lead+float64(float64(len(levels))*gap)+1.5)*rate))
	state := uint64(0xfeed)
	for i := range pcm {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		pcm[i] = math.Pow(10, -70.0/20) * math.Sqrt(3) * (float64(float64(z>>11)/float64(1<<53)*2) - 1)
	}
	for k, level := range levels {
		at := int((lead + float64(float64(k)*gap)) * rate)
		for i, x := range beatingTom(uint64(400+k), gap+1.5, beat, ratio, tau) {
			if at+i < len(pcm) {
				pcm[at+i] += float64(x * level)
			}
		}
	}
	out := make([]float32, len(pcm))
	for i, x := range pcm {
		out[i] = float32(x)
	}
	return Audio{PCM: out, Rate: rate}
}

// tomTake is a beatingTake whose first hit comes at 0.3 s.
func tomTake(gap, beat, ratio, tau float64, levels ...float64) Audio {
	return beatingTake(.3, gap, beat, ratio, tau, levels...)
}

// A tom struck again a second after the last hit lands on a loud, beating ring.
// The energy rises 2.5 times within 10 ms for two windows, and passes 1.5 times
// the ring's recent maximum two windows after that: never both at once. The hit
// must not vanish into the slice before it, which would then play as a double
// strike with no warning.
func TestRingingFindsATomStruckAgainOnABeatingRing(t *testing.T) {
	levels := []float64{.60, .55, .65, .58, .50, .62}
	for _, test := range []struct{ beat, ratio, tau float64 }{
		{4, .6, .7}, {4, .8, .7}, {8, .7, .7}, {8, .9, .7}, {12, .7, .7}, {12, .9, .7},
	} {
		t.Run(fmt.Sprintf("%.0f Hz beat, second mode at %.0f%%", test.beat, test.ratio*100), func(t *testing.T) {
			audio := tomTake(1, test.beat, test.ratio, test.tau, levels...)
			hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != len(levels) || reports[0].Merged != 0 {
				t.Errorf("found %d hits and %d merged attacks, played %d hits", len(hits), reports[0].Merged, len(levels))
			}
			for i, h := range hits {
				played := .3 + float64(i)
				if lead := leadIn(h.Start, played); lead < 0 || lead > 6 {
					t.Errorf("hit %d starts %.1f ms before its attack, played at %.3f s; want up to 6 ms", i+1, lead, played)
				}
			}
		})
	}
}

// A ring that beats slowly climbs out of each null past the loudest sound of
// the last 100 ms, since a peak a quarter of a second ago is out of sight. It
// climbs slowly, though, and is not a hit. A hit struck while it rings still is
// one, whatever the beat. So the same toms, struck one and two seconds apart,
// are found once each, and a single hit with a slow beat is not found twice.
func TestRingingBeatingTomsAreFoundOnceEach(t *testing.T) {
	levels := []float64{.60, .55, .65, .58, .50, .62}
	for _, gap := range []float64{1, 2} {
		for _, beat := range []float64{4, 12} {
			for _, ratio := range []float64{.5, .9} {
				for _, tau := range []float64{.4, .7} {
					audio := tomTake(gap, beat, ratio, tau, levels...)
					hits, _, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
					if err != nil || len(hits) != len(levels) {
						t.Errorf("%.0f s apart, %.0f Hz beat, second mode at %.0f%%, decay %.1f s: found %d hits, played %d (%v)", gap, beat, ratio*100, tau, len(hits), len(levels), err)
					}
				}
			}
		}
	}
	for _, beat := range []float64{2, 3, 5} {
		for _, ratio := range []float64{.9, 1} {
			for _, tau := range []float64{1.5, 3} {
				audio := tomTake(8, beat, ratio, tau, .6)
				hits, _, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
				if err != nil || len(hits) != 1 {
					t.Errorf("one hit, %.0f Hz beat, second mode at %.0f%%, decay %.1f s: found %d hits (%v)", beat, ratio*100, tau, len(hits), err)
				}
			}
		}
	}
}

// peakOf is the largest absolute sample of pcm in frames from to to.
func peakOf(pcm []float32, from, to int) float64 {
	peak := 0.0
	for _, x := range pcm[max(0, from):min(len(pcm), to)] {
		peak = max(peak, math.Abs(float64(x)))
	}
	return peak
}

// A cymbal struck again while the last one rings is cut off by the next hit.
// The cut must fall before the next hit's attack, and the next slice must begin
// before it: otherwise the first slice ends in a burst of the second strike, and
// the second loses the start of its own attack. The energy that finds a hit is
// smoothed over 10 ms, so it shows a swell a few milliseconds after it began.
func TestRingingCutSliceEndsBeforeTheNextAttackAndTheNextBeginsBeforeIt(t *testing.T) {
	const rate, ms = 48000, 48
	for _, spacing := range []float64{4, 6} {
		t.Run(fmt.Sprintf("%.0f s apart", spacing), func(t *testing.T) {
			audio := spaced(t, testwav.LongCrash, testwav.Layout{Lead: 1, Spacing: spacing, After: 2, RoomDB: -80}, .55, .50, .60, .52)
			hits, _, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
			if err != nil || len(hits) != 4 {
				t.Fatalf("found %d hits, played 4 (%v)", len(hits), err)
			}
			for i, h := range hits {
				// Each slice begins before its attack, but not by more than 6 ms: it
				// would otherwise play its hit late against the rest of the kit.
				if lead := leadIn(h.Start, 1+float64(i)*spacing); lead < .5 || lead > 6 {
					t.Errorf("hit %d starts %.1f ms before its attack; want .5 to 6 ms", i+1, lead)
				}
			}
			for i := 0; i+1 < len(hits); i++ {
				h, next := hits[i], hits[i+1]
				if !h.Cut {
					t.Fatalf("hit %d was not cut off; the test needs a tail that runs on under the next hit", i+1)
				}
				// A hit that the next one cut off ends where the next begins: not
				// later, which would play the same milliseconds twice.
				if h.End != next.Start {
					t.Errorf("hit %d ends at frame %d and hit %d starts at frame %d; a cut slice ends where the next one begins", i+1, h.End, i+2, next.Start)
				}
				// The last 3 ms of the cut slice, in the source, are tail: no louder
				// than the 20 ms before them, give or take the tail's own texture.
				tail := peakOf(audio.PCM, h.End-25*ms, h.End-5*ms)
				if end := peakOf(audio.PCM, h.End-3*ms, h.End); end > 2*tail {
					t.Errorf("hit %d: the last 3 ms of the slice peak at %.1f dB above the tail before them; the next attack has begun", i+1, dB(end/tail))
				}
				// The next slice begins before its hit does: its first 2 ms are
				// the tail as well, no louder than the 20 ms before them.
				before := peakOf(audio.PCM, next.Start-25*ms, next.Start-5*ms)
				if start := peakOf(audio.PCM, next.Start, next.Start+2*ms); start > 2*before {
					t.Errorf("hit %d: the slice begins %.1f dB above the tail before it, inside the attack", i+2, dB(start/before))
				}
			}
		})
	}
}

// smoothed is the energy that AnalyzeTakes looks at: the RMS of each 2 ms window,
// smoothed over 10 ms.
func smoothed(pcm []float32, rate int) []float64 {
	hop := rate / 500
	raw := make([]float64, (len(pcm)+hop-1)/hop)
	for w := range raw {
		var e float64
		end := min(len(pcm), (w+1)*hop)
		for _, x := range pcm[w*hop : end] {
			e += mul(float64(x), float64(x))
		}
		raw[w] = math.Sqrt(e / float64(end-w*hop))
	}
	out := make([]float64, len(raw))
	for w := range out {
		var sum float64
		from := max(0, w-4)
		for _, e := range raw[from : w+1] {
			sum += mul(e, e)
		}
		out[w] = math.Sqrt(sum / float64(w-from+1))
	}
	return out
}

// A tail that beats against itself swells and fades every 33 ms or so, and where
// the cut falls in the beat decides what a few milliseconds of it say. Here the
// tom's second mode is 30 Hz above the first and almost as loud, so each fade is a
// lull 20 dB or more under the lobe before it, and the hits are a second apart: the
// tail is still 3 to 12 dB over the limit for a cut to be reported when the next
// hit cuts it off. For some spacings the cut falls in a lull, where the lowest of
// the last 12 ms is far under the tail, and the cut went unreported. The tail's
// level is read over a beat, so every one of these cuts is reported, at the
// tail's level.
func TestRingingCutIsReportedOnATailThatBeatsAtTheCut(t *testing.T) {
	levels := []float64{.60, .55, .65, .58, .50}
	const rate = 48000
	var hiddenByALull, cuts int
	for _, gapMS := range []int{1000, 1010, 1020, 1030, 1040, 1050} {
		audio := beatingTake(3, float64(gapMS)/1000, 30, .95, .2, levels...)
		hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || len(hits) != len(levels) {
			t.Fatalf("hits %d ms apart: found %d (%v), played %d", gapMS, len(hits), err, len(levels))
		}
		limit := reports[0].QuietDB + 20*math.Log10(ringCutMargin)
		energy := smoothed(audio.PCM, rate)
		for i := 0; i+1 < len(hits); i++ {
			h := hits[i]
			if h.End != hits[i+1].Start {
				t.Fatalf("hits %d ms apart: hit %d ends at %d, not where hit %d starts, %d; the test needs a tail that the next hit cuts off", gapMS, i+1, h.End, i+2, hits[i+1].Start)
			}
			// The source over the 30 ms that end 4 ms before the cut, and the lowest of the
			// last 12 ms, which is what the cut level was once read from.
			var sum float64
			for _, x := range audio.PCM[h.End-34*rate/1000 : h.End-4*rate/1000] {
				sum += mul(float64(x), float64(x))
			}
			tail := 10 * math.Log10(sum/float64(30*rate/1000))
			last := (h.End - 1) / (rate / 500)
			lowest := dB(slices.Min(energy[last-5 : last+1]))
			if tail < limit+3 {
				continue // too close to the limit to ask for a report
			}
			cuts++
			if lowest < limit {
				hiddenByALull++
			}
			if !h.Cut {
				t.Errorf("hits %d ms apart: hit %d was cut at a tail of %.1f dBFS, %.1f dB over the limit of %.1f dBFS, and not reported (the lowest of its last 12 ms is %.1f dBFS)", gapMS, i+1, tail, tail-limit, limit, lowest)
			} else if math.Abs(h.CutDB-tail) > 3 {
				t.Errorf("hits %d ms apart: hit %d was cut at a tail of %.1f dBFS and reports %.1f dBFS", gapMS, i+1, tail, h.CutDB)
			}
		}
	}
	t.Logf("%d cuts 3 dB or more over the limit; %d of them fall in a lull that hides the tail from the lowest of the last 12 ms", cuts, hiddenByALull)
	// The takes must hold enough cuts, and lulls at them, or the test says nothing.
	if cuts < 15 || hiddenByALull < cuts/4 {
		t.Errorf("%d cuts are 3 dB or more over the limit, and %d of them fall in a lull that hides the tail from the lowest of the last 12 ms; the test needs 15 and a quarter", cuts, hiddenByALull)
	}
}

// A recording that starts in the middle of a ring has no room tone: the ring
// before the first hit is far above the room, though far below the loudest
// sound, and must not be taken for the room. The take here starts six seconds
// into a crash that rings for fourteen, and the hit comes two seconds later; the
// room is heard only at the end, after the ring has died.
func TestRingingTakeThatStartsMidRingHasNoRoomTone(t *testing.T) {
	whole := spaced(t, testwav.LongCrash, testwav.Layout{Lead: 0, Spacing: 8, After: 14, RoomDB: -70}, .55, .6)
	audio := Audio{PCM: whole.PCM[6*48000:], Rate: 48000}
	hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil || len(hits) == 0 {
		t.Fatalf("found %d hits (%v)", len(hits), err)
	}
	// Before the hit, the ring is 40 dB above the room, and not near the quietest stretch.
	if report := reports[0]; !report.NoRoomTone || report.NoQuiet {
		t.Errorf("no room tone %v, no quiet stretch %v; want true and false (quietest 200 ms at %.1f dBFS, loudest sound at %.1f dBFS)", report.NoRoomTone, report.NoQuiet, report.NoiseDB, report.LoudestDB)
	}
}

// The room is the quietest stretch of 200 ms. A dip of a few tens of
// milliseconds in a ring is not the room: the take here is all tail, with a gap
// of silence cut into it, and its noise is read from stretches of 200 ms, which
// hold a good deal of tail.
func TestRingingRoomIsTheQuietestTwoHundredMillisecondsNotAGap(t *testing.T) {
	whole := spaced(t, testwav.LongCrash, testwav.Layout{Spacing: 4, After: 1, RoomDB: -70}, .55, .5, .6, .52)
	pcm := append([]float32(nil), whole.PCM...)
	for i := 2*48000 + 1000; i < 2*48000+1000+48*50; i++ { // 50 ms of silence, 2 s into the first tail
		pcm[i] = 0
	}
	_, reports, err := AnalyzeTakes([]Audio{{PCM: pcm, Rate: 48000}}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil {
		t.Fatal(err)
	}
	// The tail is about 40 dB below the hit; a 20 ms look at the gap would read -240 dBFS.
	if reports[0].NoiseDB < reports[0].LoudestDB-45 {
		t.Errorf("the noise is read at %.1f dBFS, %.1f dB below the loudest sound: from the gap, not from 200 ms of tail", reports[0].NoiseDB, reports[0].LoudestDB-reports[0].NoiseDB)
	}
}

// endedTake is a take and a name for the way it ends.
type endedTake struct {
	name string
	pcm  []float32
}

// roomToneAndThenSilence returns a take of five kick hits with 3 s of room tone
// at -70 dBFS before the first, and the ways such a take can end quieter than
// the room: silence appended after the last tail, as an export that runs past
// the clip adds, the last 0.3 s zeroed, or a fade-out of 1 or 2 s.
func roomToneAndThenSilence(t *testing.T) (plain Audio, ends []endedTake) {
	t.Helper()
	plain = spaced(t, testwav.Kick, testwav.Layout{Lead: 3, Spacing: 1.5, After: 2, RoomDB: -70}, .55, .50, .60, .52, .58)
	with := func(name string, edit func(pcm []float32) []float32) {
		ends = append(ends, endedTake{name, edit(append([]float32(nil), plain.PCM...))})
	}
	fade := func(seconds float64) func([]float32) []float32 {
		return func(pcm []float32) []float32 {
			n := int(seconds * 48000)
			for i := 0; i < n; i++ {
				g := float64(n-i) / float64(n)
				pcm[len(pcm)-n+i] *= float32(g * g * g)
			}
			return pcm
		}
	}
	with("0.3 s of silence added", func(pcm []float32) []float32 { return append(pcm, make([]float32, 14400)...) })
	with("5 s of silence added", func(pcm []float32) []float32 { return append(pcm, make([]float32, 5*48000)...) })
	with("the last 0.3 s silent", func(pcm []float32) []float32 {
		clear(pcm[len(pcm)-14400:])
		return pcm
	})
	with("a 1 s fade-out", fade(1))
	with("a 2 s fade-out", fade(2))
	return plain, ends
}

// A take that opens with room tone has room tone, whatever follows the last
// tail. Silence after it, or a fade-out, is quieter than any room, so the quietest
// 200 ms of the take is not the room: it was read as -240 dBFS, and the take was
// told it had no room tone and should start with the silence it already had.
// The lead-in holds steady, as a ring that is dying away does not, so it is the room.
func TestRingingTakeThatEndsQuieterThanTheRoomStillOpensWithRoomTone(t *testing.T) {
	plain, ends := roomToneAndThenSilence(t)
	want, plainReports, err := AnalyzeTakes([]Audio{plain}, Options{Root: 36, Layers: 1, Ringing: true})
	if err != nil || len(want) != 5 || plainReports[0].NoRoomTone {
		t.Fatalf("the take without such an end: %d hits, %+v, %v", len(want), plainReports, err)
	}
	for _, end := range ends {
		t.Run(end.name, func(t *testing.T) {
			hits, reports, err := AnalyzeTakes([]Audio{{PCM: end.pcm, Rate: 48000}}, Options{Root: 36, Layers: 1, Ringing: true})
			if err != nil || len(hits) != len(want) {
				t.Fatalf("found %d hits (%v), played %d", len(hits), err, len(want))
			}
			report := reports[0]
			if report.NoRoomTone || report.NoQuiet {
				t.Errorf("no room tone %v, no quiet stretch %v; the take opens with 3 s of room tone", report.NoRoomTone, report.NoQuiet)
			}
			// The room is the lead-in's, -70 dBFS, and tails finish 6 dB above it as
			// they do in the take without the end.
			if math.Abs(report.NoiseDB+70) > 3 || math.Abs(report.QuietDB-report.NoiseDB-6) > 1 {
				t.Errorf("room read at %.1f dBFS (it is at -70), tails finish at %.1f dBFS", report.NoiseDB, report.QuietDB)
			}
			// Everything before the end is the same take, so its hits are the same.
			for i := range want[:4] {
				if hits[i].Start != want[i].Start || hits[i].End != want[i].End || hits[i].Cut != want[i].Cut {
					t.Errorf("hit %d runs from frame %d to %d (cut %v); without the end it runs from %d to %d (cut %v)", i+1, hits[i].Start, hits[i].End, hits[i].Cut, want[i].Start, want[i].End, want[i].Cut)
				}
			}
		})
	}
}

// The lead-in is the room only when it can be told from a ring that is still
// dying away, which takes a second of it that holds steady; and a take must
// still open with it. These takes end in silence too, and all report no room
// tone: one that starts with a hit, one that starts a second into the ring of a
// crash, and one with only half a second of room tone before its first hit.
func TestRingingTakeThatEndsInSilenceHasNoRoomToneUnlessItOpensWithSteadyRoomTone(t *testing.T) {
	silence := func(pcm []float32) Audio {
		return Audio{PCM: append(append([]float32(nil), pcm...), make([]float32, 14400)...), Rate: 48000}
	}
	whole := spaced(t, testwav.LongCrash, testwav.Layout{Lead: 0, Spacing: 8, After: 14, RoomDB: -70}, .55, .6)
	for _, test := range []struct {
		name  string
		audio Audio
		tone  bool
	}{
		{"opens with a hit", silence(spaced(t, testwav.Kick, testwav.Layout{Spacing: 1.5, After: 2, RoomDB: -70}, .55, .50, .60).PCM), false},
		{"opens in the middle of a ring", silence(whole.PCM[6*48000:]), false},
		{"opens with half a second of room tone", silence(spaced(t, testwav.Kick, testwav.Layout{Lead: .5, Spacing: 1.5, After: 2, RoomDB: -70}, .55, .50, .60).PCM), false},
		{"opens with 1.2 s of room tone", silence(spaced(t, testwav.Kick, testwav.Layout{Lead: 1.2, Spacing: 1.5, After: 2, RoomDB: -70}, .55, .50, .60).PCM), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, reports, err := AnalyzeTakes([]Audio{test.audio}, Options{Root: 36, Layers: 1, Ringing: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := !reports[0].NoRoomTone; got != test.tone {
				t.Errorf("room tone %v, want %v (the room was read at %.1f dBFS, the loudest sound at %.1f dBFS)", got, test.tone, reports[0].NoiseDB, reports[0].LoudestDB)
			}
		})
	}
}

// Drums recorded as the guide asks, about a second apart in a quiet room, ring
// out almost all of that second, and the tail still beats against itself when
// the next hit comes. Each time the beat climbs out of a lull the energy steps
// up, in a bump 30 dB or more below the hit, and for some spacings the bump falls
// 8 to 24 ms before a hit. A slice that began at such a bump started up to 24 ms
// before its hit, so that hit played that much later than the others of its
// kind. Every slice must start a few milliseconds before its attack, with the
// same lead-in whatever its tail was doing. The spacings are those at which a
// sweep from 0.90 to 0.96 s put a bump in front of a hit.
func TestRingingSlicesStartAtTheirAttackWhateverTheTailDoesBeforeIt(t *testing.T) {
	levels := []float64{.60, .55, .65, .58, .50}
	const rate = 48000
	for _, test := range []struct {
		beat, ratio, tau float64
		gapsMS           []int
	}{
		{20, .95, .22, []int{900, 906, 909, 921, 960}},
		{30, .7, .22, []int{918, 939}},
		{12, .95, .22, []int{936}},
	} {
		t.Run(fmt.Sprintf("%.0f Hz beat, second mode at %.0f%%, decay %.2f s", test.beat, test.ratio*100, test.tau), func(t *testing.T) {
			for _, ms := range test.gapsMS {
				gap := float64(ms) / 1000
				audio := beatingTake(3, gap, test.beat, test.ratio, test.tau, levels...)
				hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(hits) != len(levels) || reports[0].Merged != 0 {
					t.Errorf("hits %.3f s apart: found %d and merged %d, played %d", gap, len(hits), reports[0].Merged, len(levels))
					continue
				}
				earliest, latest := math.Inf(1), math.Inf(-1)
				for i, h := range hits {
					played := int((3 + float64(float64(i)*gap)) * rate)
					lead := float64(played-h.Start) * 1000 / rate
					earliest, latest = min(earliest, lead), max(latest, lead)
					if lead < 2 || lead > 6 {
						t.Errorf("hits %.3f s apart: hit %d starts %.1f ms before its attack, want 2 to 6 ms", gap, i+1, lead)
					}
				}
				if latest-earliest > 2.1 {
					t.Errorf("hits %.3f s apart: the hits start %.1f to %.1f ms before their attacks, a spread of %.1f ms", gap, earliest, latest, latest-earliest)
				}
			}
		})
	}
}

// twoStageTake places five hits of a drum whose attack comes in two stages,
// spacing seconds apart after 3 s of room tone at -70 dBFS. A hit begins with a
// first stage at stageDB below its body, and the body follows gapMS later: a 70
// Hz sine that decays over 120 ms. The first stage is a click, a 2 ms burst of
// noise, or a soft stage, a 70 Hz sine that lasts until the body starts. It
// returns the take and the frame at which each hit begins, which is when its first
// stage does. The hits start a few frames apart from the 2 ms windows of the
// analysis, a different few each time.
func twoStageTake(spacing, gapMS, stageDB float64, click bool) (Audio, []int) {
	const rate = 48000
	pcm := make([]float64, int((3+spacing*4+1.5)*rate))
	state := uint64(0xfeed)
	noise := func() float64 { // splitmix64, as testwav uses
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		return float64(float64(z>>11)/float64(1<<53)*2) - 1
	}
	room := math.Pow(10, -70.0/20) * math.Sqrt(3)
	for i := range pcm {
		pcm[i] = float64(room * noise())
	}
	stage := math.Pow(10, stageDB/20)
	var plays []int
	for k, level := range []float64{.5, .45, .55, .48, .52} {
		at := int((3+float64(float64(k)*spacing))*rate) + k*29
		plays = append(plays, at)
		body := at + int(gapMS/1000*rate)
		// Every product is rounded before it is added, as mul does for the analysis,
		// so the recording is the same on a processor that fuses x*y+z.
		if click {
			for i := 0; i < rate/500; i++ {
				pcm[at+i] += float64(float64(level*stage) * float64(math.Sqrt(3)*noise()))
			}
		} else {
			for i := 0; i < body-at; i++ {
				t := float64(i) / rate
				pcm[at+i] += float64(float64(level*stage) * math.Sin(float64(2*math.Pi*70*t)))
			}
		}
		for i := 0; i < rate*6/10; i++ {
			t := float64(i) / rate
			attack := min(1, float64(t/.001))
			pcm[body+i] += float64(float64(level*float64(attack*math.Exp(-t/.12))) * math.Sin(float64(2*math.Pi*70*t)))
		}
	}
	out := make([]float32, len(pcm))
	for i, x := range pcm {
		out[i] = float32(x)
	}
	return Audio{PCM: out, Rate: rate}, plays
}

// sliceLeads analyzes a take of hits at the frames plays and returns how many
// milliseconds before its hit each slice starts.
func sliceLeads(t *testing.T, audio Audio, plays []int) []float64 {
	t.Helper()
	hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(plays) || reports[0].Merged != 0 {
		t.Fatalf("found %d hits and merged %d attacks, played %d hits", len(hits), reports[0].Merged, len(plays))
	}
	leads := make([]float64, len(hits))
	for i, h := range hits {
		leads[i] = float64(plays[i]-h.Start) * 1000 / float64(audio.Rate)
	}
	return leads
}

// Some drums open with a click, a stick or a beater touching the head, a few
// milliseconds before the body. The sampler should play the click with the body,
// as the drum did, so the slice starts at the click. A tail that steps up out of a
// lull on its own does not belong to the hit (the beating-tom takes above check
// that), but a click comes out of quiet, and a bump in a tail does not: the click
// is four times as loud as the 12 ms before it, and eight times as loud when it is
// far from the body. These takes put a click 18 to 32 dB under the body, 4 to 14
// ms ahead of it. A slice began at the body when the click was more than 4 ms
// ahead, 1.5 to 3.8 ms after the click on recorded snares, and when the click was
// under a twentieth of the body, at any distance.
func TestRingingSliceBeginsWithAClickThatComesBeforeTheBody(t *testing.T) {
	for _, gapMS := range []float64{4, 5, 6, 7, 8, 10, 12, 14} {
		for _, clickDB := range []float64{-18, -24, -32} {
			t.Run(fmt.Sprintf("click %.0f dB down, %.0f ms ahead", -clickDB, gapMS), func(t *testing.T) {
				audio, plays := twoStageTake(1.5, gapMS, clickDB, true)
				for i, lead := range sliceLeads(t, audio, plays) {
					if lead < 2 || lead > 6 {
						t.Errorf("hit %d starts %.1f ms before its click, want 2 to 6 ms (the body follows %.0f ms after it)", i+1, lead, gapMS)
					}
				}
			})
		}
	}
	// A sound that far ahead of the body is a sound of its own: it is under the
	// level at which a hit is found, so the hit is the body, and its slice starts 3
	// to 5 ms before that.
	t.Run("a click 20 ms ahead is left out", func(t *testing.T) {
		audio, plays := twoStageTake(1.5, 20, -24, true)
		for i, lead := range sliceLeads(t, audio, plays) {
			if body := lead + 20; body < 2 || body > 6 {
				t.Errorf("hit %d starts %.1f ms before its body, want 2 to 6 ms: the click 20 ms ahead is not part of the hit", i+1, body)
			}
		}
	})
}

// The first stage of an attack can be a soft rise instead of a click: a muted bass
// drum starts with a thud 25 to 30 dB under the body, 6 to 8 ms before it. The stage
// stays under a twentieth of the body, so the slice began at the body, 3 to 5 ms
// after the hit had started. The stage comes out of quiet as a sharp step, so it is
// part of the hit. These takes put a stage 26 to 34 dB under the body, 6 to 8 ms
// ahead of it.
func TestRingingSliceBeginsWithASoftFirstStageThatComesBeforeTheBody(t *testing.T) {
	for _, gapMS := range []float64{6, 7, 8} {
		for _, stageDB := range []float64{-26, -30, -34} {
			t.Run(fmt.Sprintf("stage %.0f dB down, %.0f ms ahead", -stageDB, gapMS), func(t *testing.T) {
				audio, plays := twoStageTake(1.5, gapMS, stageDB, false)
				for i, lead := range sliceLeads(t, audio, plays) {
					if lead < 2 || lead > 6 {
						t.Errorf("hit %d starts %.1f ms before its first stage, want 2 to 6 ms (the body follows %.0f ms after it)", i+1, lead, gapMS)
					}
				}
			})
		}
	}
}

// withClick returns a copy of pcm with a 10 ms burst of noise added at the given
// level in dBFS (RMS), starting at the given second: the click of a recorder that
// has just started, a knock on the stand.
func withClick(pcm []float32, at, db float64) []float32 {
	out := slices.Clone(pcm)
	state := uint64(0x77)
	amp := math.Pow(10, db/20) * math.Sqrt(3)
	for i := int(at * 48000); i < int(at*48000)+480; i++ {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		noise := float64(float64(z>>11)/float64(1<<53)*2) - 1
		out[i] = float32(float64(out[i]) + float64(amp*noise))
	}
	return out
}

// A recording often opens with a noise: the click of the recorder starting, a
// knock on the stand, a breath. It lies in the first 200 ms of room tone, and it is
// not a ring that is dying away, which stays louder than the room for as long as
// it lasts. The take here ends in silence or a fade-out, so its room is read from
// its lead-in, and the lead-in holds steady in all but the first stretch. It was
// told it had no room tone, and that its room was at -240 dBFS.
func TestRingingRoomToneIsFoundAfterANoiseAtTheStartOfTheTake(t *testing.T) {
	_, ends := roomToneAndThenSilence(t)
	for _, click := range []struct {
		name  string
		at    float64
		level float64
	}{
		{"a click at 50 ms, -45 dBFS", .05, -45},
		{"a click at 50 ms, -55 dBFS", .05, -55},
		{"a click at 150 ms, -45 dBFS", .15, -45},
		{"a click at 350 ms, -45 dBFS", .35, -45},
	} {
		for i, end := range ends {
			t.Run(click.name+", "+end.name, func(t *testing.T) {
				want, _, err := AnalyzeTakes([]Audio{{PCM: end.pcm, Rate: 48000}}, Options{Root: 36, Layers: 1, Ringing: true})
				if err != nil || len(want) != 5 {
					t.Fatalf("the take without the click: %d hits, %v", len(want), err)
				}
				audio := Audio{PCM: withClick(ends[i].pcm, click.at, click.level), Rate: 48000}
				hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
				if err != nil || len(hits) != len(want) {
					t.Fatalf("found %d hits (%v), played %d", len(hits), err, len(want))
				}
				report := reports[0]
				if report.NoRoomTone || report.NoQuiet {
					t.Errorf("no room tone %v, no quiet stretch %v; the take opens with 3 s of room tone and a click", report.NoRoomTone, report.NoQuiet)
				}
				if math.Abs(report.NoiseDB+70) > 3 || math.Abs(report.QuietDB-report.NoiseDB-6) > 1 {
					t.Errorf("room read at %.1f dBFS (it is at -70), tails finish at %.1f dBFS", report.NoiseDB, report.QuietDB)
				}
				for j := range want[:4] {
					if hits[j].Start != want[j].Start || hits[j].End != want[j].End || hits[j].Cut != want[j].Cut {
						t.Errorf("hit %d runs from frame %d to %d (cut %v); without the click it runs from %d to %d (cut %v)", j+1, hits[j].Start, hits[j].End, hits[j].Cut, want[j].Start, want[j].End, want[j].Cut)
					}
				}
			})
		}
	}
	// A noise that lasts through the first 0.5 s leaves no 200 ms of quiet to judge
	// the lead-in by, and the take is told it has no room tone: the safe answer.
	t.Run("a noise that lasts 0.45 s", func(t *testing.T) {
		pcm := slices.Clone(ends[0].pcm)
		for k := 0; k < 9; k++ {
			pcm = withClick(pcm, .05*float64(k), -45)
		}
		_, reports, err := AnalyzeTakes([]Audio{{PCM: pcm, Rate: 48000}}, Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || !reports[0].NoRoomTone {
			t.Errorf("no room tone %v (%v); a noise through the first 0.45 s leaves no quiet stretch to judge by", reports[0].NoRoomTone, err)
		}
	})
}

// padded returns a copy of audio with frames of digital silence in front.
func padded(audio Audio, seconds float64) Audio {
	return Audio{PCM: append(make([]float32, int(seconds*48000)), audio.PCM...), Rate: audio.Rate}
}

// An export that starts before the clip begins with digital silence, which is no
// room: the room is the tone that follows it. Read from the whole take, the quietest
// 200 ms is the silence, at -240 dBFS, and no tail ever came down to it: every hit
// was cut off by the next, held for 1.5 s instead of 0.47 s. The room is the same
// whether or not the silence comes first.
func TestRingingRoomToneIsFoundAfterDigitalSilenceAtTheStartOfTheTake(t *testing.T) {
	plain := spaced(t, testwav.Kick, testwav.Layout{Lead: 3, Spacing: 1.5, After: 2, RoomDB: -60}, .55, .50, .60, .52, .58)
	want, wantReports, err := AnalyzeTakes([]Audio{plain}, Options{Root: 36, Layers: 1, Ringing: true})
	if err != nil || len(want) != 5 || wantReports[0].NoRoomTone || want[0].Cut {
		t.Fatalf("the take without the silence: %d hits, %+v, %v", len(want), wantReports[0], err)
	}
	for _, seconds := range []float64{.1, .3, .5, 1, 2} {
		t.Run(fmt.Sprintf("%.1f s of silence", seconds), func(t *testing.T) {
			audio := padded(plain, seconds)
			hits, reports, err := AnalyzeTakes([]Audio{audio}, Options{Root: 36, Layers: 1, Ringing: true})
			if err != nil || len(hits) != len(want) {
				t.Fatalf("found %d hits (%v), played %d", len(hits), err, len(want))
			}
			report := reports[0]
			if report.NoRoomTone || report.NoQuiet || math.Abs(report.NoiseDB+60) > 3 || math.Abs(report.QuietDB-report.NoiseDB-6) > 1 {
				t.Errorf("no room tone %v, no quiet stretch %v, room read at %.1f dBFS (it is at -60), tails finish at %.1f dBFS", report.NoRoomTone, report.NoQuiet, report.NoiseDB, report.QuietDB)
			}
			pad := int(seconds * 48000)
			for i, h := range hits {
				if h.Start != want[i].Start+pad || h.End != want[i].End+pad || h.Cut != want[i].Cut {
					t.Errorf("hit %d runs from frame %d to %d (cut %v); without the silence it runs from %d to %d (cut %v), %d frames earlier", i+1, h.Start, h.End, h.Cut, want[i].Start+pad, want[i].End+pad, want[i].Cut, pad)
				}
			}
		})
	}
	// A recording gated to silence has digital silence for its room, between the
	// hits as well as before the first: the silence is the room tone, and it is read as it is.
	t.Run("a recording gated to silence", func(t *testing.T) {
		gated := slices.Clone(plain.PCM)
		for i := range gated {
			live := false
			for k := 0; k < 5; k++ {
				if from := int((3 + 1.5*float64(k)) * 48000); i >= from && i < from+int(.45*48000) {
					live = true
				}
			}
			if !live {
				gated[i] = 0
			}
		}
		hits, reports, err := AnalyzeTakes([]Audio{{PCM: gated, Rate: 48000}}, Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || len(hits) != 5 {
			t.Fatalf("found %d hits (%v), played 5", len(hits), err)
		}
		if reports[0].NoRoomTone || reports[0].NoiseDB > -200 {
			t.Errorf("no room tone %v, room read at %.1f dBFS; the silence before the first hit is the room of a gated recording", reports[0].NoRoomTone, reports[0].NoiseDB)
		}
		for i, h := range hits {
			if h.Cut {
				t.Errorf("hit %d was cut off at %.1f dBFS; it ends in the silence of the gate", i+1, h.CutDB)
			}
		}
	})
}

// roomToneTake is the energy of 2 ms windows that AnalyzeTakes passes to roomTone:
// the lead windows, then a hit that dies away within five windows, then the tail
// windows; the loudest window, and the quietest stretch of 200 ms.
func roomToneTake(lead []float64, tail ...float64) (energy []float64, loudest, quietest float64) {
	hit := []float64{.5, .3, .2, .1, .05}
	energy = slices.Concat(lead, hit, tail)
	return energy, slices.Max(energy), quietestStretch(energy, 100)
}

// The lead-in is the room when it lasts a second, holds steady, and the take ends
// quieter than any room: steady is judged from the quietest 200 ms within its first
// 0.5 s, so a noise near the start does not matter and a ring that is dying away
// still does. Digital silence before it is no part of it.
func TestRoomToneIsJudgedFromTheQuietestOfTheFirstHalfSecond(t *testing.T) {
	const room = 1e-3
	tone := func(windows int) []float64 { return slices.Repeat([]float64{room}, windows) }
	level := func(v float64, windows int) []float64 { return slices.Repeat([]float64{v}, windows) }
	silence := func(windows int) []float64 { return make([]float64, windows) }
	// ring decays db dB each second from level at its start, 2 ms a window.
	ring := func(windows int, start, db float64) []float64 {
		out := make([]float64, windows)
		for i := range out {
			out[i] = start * math.Pow(10, -db*float64(i)*.002/20)
		}
		return out
	}
	noisy := func(windows int, from, to int) []float64 { // room tone with a noise in windows [from, to)
		out := tone(windows)
		for i := from; i < to; i++ {
			out[i] = 20 * room
		}
		return out
	}
	for _, test := range []struct {
		name string
		lead []float64
		tail []float64 // what comes after the hit
		tone bool
		room float64 // the room read, when there is tone
	}{
		{"a second and a half of room tone", tone(750), silence(150), true, room},
		{"a lead-in of 1 s", tone(500), silence(150), true, room},
		{"a lead-in under 1 s", tone(450), silence(150), false, 0},
		{"a click at 50 ms", noisy(750, 25, 30), silence(150), true, room},
		// The first half second is judged against the quietest stretch of the lead-in
		// with a factor of 1.3, 2.3 dB: 1.26 times as loud passes and 1.4 does not.
		{"a first half second 2 dB louder than the rest", slices.Concat(level(1.26*room, 250), tone(500)), silence(150), true, room},
		{"a first half second 2.9 dB louder than the rest", slices.Concat(level(1.4*room, 250), tone(500)), silence(150), false, 0},
		{"a noise through the first 300 ms", noisy(750, 10, 150), silence(150), true, room},
		{"a noise through the first 340 ms", noisy(750, 10, 170), silence(150), false, 0},
		{"a noise at 350 ms", noisy(750, 175, 180), silence(150), true, room},
		{"a noise at 1 s", noisy(750, 500, 520), silence(150), true, room},
		{"a ring that fades 10 dB in 1.5 s", ring(750, 10*room, 6.7), silence(150), false, 0},
		{"a ring that fades 40 dB in 1.5 s", ring(750, 100*room, 26.7), silence(150), false, 0},
		{"a ring that fades 2 dB in 1.5 s", ring(750, 1.26*room, 1.33), silence(150), true, 0},
		// The silence before the clip is not a lead-in.
		{"500 ms of silence, then room tone", slices.Concat(silence(250), tone(750)), silence(150), true, room},
		{"500 ms of silence, then 1 s of room tone", slices.Concat(silence(250), tone(500)), silence(150), true, room},
		{"500 ms of silence, then 200 ms of room tone", slices.Concat(silence(250), tone(100)), tone(150), true, room},
		// Digital silence is under -120 dBFS: a start that is quiet, but not that, is a room.
		{"500 ms at -66 dBFS, then room tone at -60 dBFS", slices.Concat(level(room/2, 250), tone(500)), silence(150), true, room / 2},
		{"500 ms at -160 dBFS, then room tone", slices.Concat(level(1e-8, 250), tone(500)), silence(150), true, room},
		{"500 ms of silence, then under 1 s of room tone", slices.Concat(silence(250), tone(450)), silence(150), false, 0},
		{"500 ms of silence, then a ring", slices.Concat(silence(250), ring(750, 10*room, 6.7)), silence(150), false, 0},
		// A lead-in a little louder than the quietest stretch of the recording is read at that stretch.
		{"a lead-in 3 dB louder than the end of the recording", level(1.4*room, 750), tone(150), true, room},
		// A recording that was gated to silence has the silence for its room.
		{"a lead-in of silence alone", silence(750), silence(150), true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			energy, loudest, quietest := roomToneTake(test.lead, test.tail...)
			got, ok := roomTone(energy, loudest, 100, quietest)
			if ok != test.tone {
				t.Fatalf("room tone %v, want %v", ok, test.tone)
			}
			if ok && test.room > 0 && math.Abs(got-test.room)/test.room > .05 {
				t.Errorf("room read at %g, want %g", got, test.room)
			}
		})
	}
}
