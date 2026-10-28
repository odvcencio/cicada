package recording

import (
	"fmt"
	"math"
	"reflect"
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

// spaced decodes a take made by testwav.Spaced.
func spaced(t *testing.T, d testwav.Drum, layout testwav.Layout, levels ...float64) Audio {
	t.Helper()
	audio, err := DecodeWAV(testwav.Spaced(48000, d, layout, levels...))
	if err != nil {
		t.Fatal(err)
	}
	return audio
}

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
				if start := float64(h.Start) / 48000; math.Abs(start-played) > .01 {
					t.Errorf("hit %d starts at %.3f s, played at %.3f s", i+1, start, played)
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
				if !h.Cut || h.CutDB < report.QuietDB+10 || h.CutDB > h.LoudnessDB-6 {
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
		if played := float64(i) * 4; math.Abs(float64(h.Start)/48000-played) > .01 {
			t.Errorf("hit %d starts at %.3f s, played at %.1f s", i+1, float64(h.Start)/48000, played)
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
		if start := float64(h.Start) / 48000; math.Abs(start-(.2+float64(i)*2)) > .01 {
			t.Errorf("hit %d starts at %.3f s, played at %.3f s", i+1, start, .2+float64(i)*2)
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
			if start := float64(h.Start) / 48000; start < 1+float64(i)*spacing-.005 || start > 1+float64(i)*spacing+.02 {
				t.Errorf("hits %.0f s apart: hit %d starts at %.3f s, played at %.3f s", spacing, i+1, start, 1+float64(i)*spacing)
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
