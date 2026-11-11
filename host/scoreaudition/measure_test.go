package scoreaudition

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// alternateLow: two sines in the 40-80 Hz band take turns by bar and both
// play in bars 4 and 8. Two high tracks repeat one bar: steady exactly,
// human with humanize, so their played velocities differ from bar to bar.
const alternateLow = `cicada 2
title "Alternate"
tempo 120
key a minor
seed 7

instrument low {
  voice mono {
    out = sine(pitch) * adsr(gate, 5ms, 10ms, 1, 20ms) * velocity * 0.4
  }
}
instrument tick {
  voice poly {
    out = sine(pitch) * env(gate, 60ms) * velocity * 0.05
  }
}

track one low {}
track two low {}
track steady tick {}
track human tick {}

pattern one-line notes {
  gate = 100%
  a1 - - - - - - - - - - - - - - - . . . . . . . . . . . . . . . . a1 - - - - - - - - - - - - - - - a1 - - - - - - - - - - - - - - -
}
pattern two-line notes {
  gate = 100%
  . . . . . . . . . . . . . . . . g1 - - - - - - - - - - - - - - - . . . . . . . . . . . . . . . . g1 - - - - - - - - - - - - - - -
}
pattern beat notes {
  a5 . . . a5 . . . a5 . . . a5 . . .
}
pattern loose notes {
  humanize = 20
  a5 . . . a5 . . . a5 . . . a5 . . .
}
scene s {
  one = one-line
  two = two-line
  steady = beat
  human = loose
}
song { s*8 }
`

// levels: one steady tone; the second section plays it at half the velocity,
// 6.02 dB (so 6.02 LU) quieter.
const levels = `cicada 2
title "Levels"
tempo 120
key a minor
seed 1

instrument tone {
  voice mono {
    out = sine(pitch) * adsr(gate, 5ms, 10ms, 1, 20ms) * velocity * 0.2
  }
}

track bed tone {}

pattern full notes {
  gate = 100%
  a3 - - - - - - - - - - - - - - -
  velocity: 100 . . . . . . . . . . . . . . .
}
pattern half notes {
  gate = 100%
  a3 - - - - - - - - - - - - - - -
  velocity: 50 . . . . . . . . . . . . . . .
}
scene loud {
  bed = full
}
scene soft {
  bed = half
}
song { loud*2 soft*2 }
`

// gaps: a quiet bed, one loud hit at bar 1 beat 3 (tick 1920), a silent bar
// 2, then the bed again.
const gaps = `cicada 2
title "Gaps"
tempo 120
key a minor
seed 1

instrument tone {
  voice mono {
    out = sine(pitch) * adsr(gate, 5ms, 10ms, 1, 10ms) * velocity * 0.05
  }
}
instrument knock {
  voice poly {
    out = sine(pitch) * env(gate, 40ms) * velocity * 0.9
  }
}

track bed tone {}
track hit knock {}

pattern held notes {
  gate = 100%
  a3 - - - - - - - - - - - - - - -
}
pattern one-hit notes {
  . . . . . . . . a4 . . . . . . .
}
scene first {
  bed = held
  hit = one-hit
}
scene gap {
  bed = stop
  hit = stop
}
scene last {
  bed = held
}
song { first gap last }
`

func analyzeWith(t *testing.T, source string, opts Options) *Analysis {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("parse: %s", d.Error())
		}
	}
	opts.TempDir = t.TempDir()
	a, err := Analyze(score, opts)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func bandIndex(t *testing.T, name string) int {
	t.Helper()
	for i, n := range bandNames {
		if n == name {
			return i
		}
	}
	t.Fatalf("no band %s", name)
	return -1
}

func TestLowBandOwnersAlternate(t *testing.T) {
	a := analyzeWith(t, alternateLow, Options{})
	t.Logf("%s", textOf(a))
	low := bandIndex(t, "low")
	want := []string{"one", "two", "one", "", "one", "two", "one", ""}
	for b, bar := range a.Report.Bars {
		o := bar.Bands[low]
		if want[b] == "" {
			// Both sines play: each holds about half of the band.
			if !o.Collision || o.SecondSharePct <= collisionSharePct {
				t.Errorf("bar %d: %+v, want a collision", bar.Bar, o)
			}
			continue
		}
		if o.Owner != want[b] || o.SharePct < 95 || o.Collision {
			t.Errorf("bar %d: %+v, want %s owning over 95%% with no collision", bar.Bar, o, want[b])
		}
	}
	c := a.Report.Bands.Collisions[low]
	if len(c.Bars) != 2 || c.Bars[0] != 4 || c.Bars[1] != 8 || len(c.Pairs) != 1 || c.Pairs[0].Tracks != [2]string{"one", "two"} || c.Pairs[0].Bars != 2 {
		t.Errorf("low collisions %+v, want bars 4 and 8, pair one+two", c)
	}
	if got := a.Report.Sections[0].Bands[low].CollisionBars; got != 2 {
		t.Errorf("section collision bars %d, want 2", got)
	}
	// A sine's band level is its RMS level: compare with the stem's RMS over
	// the bars where it plays (both within 0.5 dB).
	one := track(t, a, "one")
	if d := one.BandsDB[low] - one.RMSdBFS; math.Abs(d) > 0.5 {
		t.Errorf("one: low band %.1f dB, stem RMS %.1f dBFS", one.BandsDB[low], one.RMSdBFS)
	}
	// Activity: one sounds in bars 1, 3, 4, 5, 7, 8; two in 2, 4, 6, 8.
	for b, on := range one.Sounding {
		if want := b%4 != 1; on != want {
			t.Errorf("one sounding in bar %d: %t", b+1, on)
		}
	}
	two := track(t, a, "two")
	for b, on := range two.Sounding {
		if want := b%2 == 1; on != want {
			t.Errorf("two sounding in bar %d: %t", b+1, on)
		}
	}
	// Repeats come from the played notes: humanize changes velocities.
	steady, human := track(t, a, "steady"), track(t, a, "human")
	if steady.NoteBars != 8 || steady.SameAsPreviousBar != 7 || steady.SameAsFourBarsBefore != 4 || steady.LongestSameRun != 8 {
		t.Errorf("steady repeats %d %d %d %d, want 8 7 4 8", steady.NoteBars, steady.SameAsPreviousBar, steady.SameAsFourBarsBefore, steady.LongestSameRun)
	}
	if human.NoteBars != 8 || human.SameAsPreviousBar > 1 {
		t.Errorf("human repeats: %d bars, %d equal the bar before; humanize should vary them", human.NoteBars, human.SameAsPreviousBar)
	}
	// one holds the same note in bars 1, 3, 4, 5, 7 and 8: bars 4, 5 and 8
	// equal the bar before, bars 5, 7 and 8 the bar four before.
	if one.NoteBars != 6 || one.SameAsPreviousBar != 3 || one.SameAsFourBarsBefore != 3 || one.LongestSameRun != 3 {
		t.Errorf("one repeats %d %d %d %d, want 6 3 3 3", one.NoteBars, one.SameAsPreviousBar, one.SameAsFourBarsBefore, one.LongestSameRun)
	}
	// The 40-80 Hz band holds the content; the others hold window leakage,
	// attack and release clicks and the quiet ticks. Above 80 Hz they are over
	// 30 dB under it and have no owner. In 20-40 Hz the frames on the bar line,
	// where one sine stops and the other starts, catch the clicks at 25 to
	// 30 dB under the 40-80 Hz band.
	sub := bandIndex(t, "sub")
	for _, bar := range a.Report.Bars {
		for k, o := range bar.Bands {
			if k == sub && bar.Bands[low].LevelDB-o.LevelDB < 25 || k != low && k != sub && !o.Quiet {
				t.Errorf("bar %d: band %s %+v is not quiet", bar.Bar, o.Band, o)
			}
		}
	}
}

func TestSectionSixLUQuieter(t *testing.T) {
	a := analyzeWith(t, levels, Options{})
	t.Logf("%s", textOf(a))
	r := a.Report
	if len(r.Sections) != 2 || r.Sections[0].IntegratedLUFS == nil || r.Sections[1].IntegratedLUFS == nil {
		t.Fatalf("sections %+v", r.Sections)
	}
	// The meter is exact for a steady tone; rounding to 0.1 LU on each side
	// gives at most 0.1 of error, the 5 ms attack of each section a little.
	diff := *r.Sections[0].IntegratedLUFS - *r.Sections[1].IntegratedLUFS
	if math.Abs(diff-6.02) > 0.2 {
		t.Errorf("loud minus soft %.2f LU, want 6.02", diff)
	}
	if r.Loudness.LoudestSection != "loud" || r.Loudness.QuietestSection != "soft" || r.Sections[1].VsLoudestLU == nil || math.Abs(*r.Sections[1].VsLoudestLU+6.02) > 0.2 {
		t.Errorf("loudness %+v, soft vs loudest %v", r.Loudness, r.Sections[1].VsLoudestLU)
	}
	if len(r.Loudness.ShortTermPerBeatLUFS) != 16 {
		t.Errorf("%d beats, want 16", len(r.Loudness.ShortTermPerBeatLUFS))
	}
	// Short-term windows end at the bar line: bar 1 (2 s) has none, bar 2
	// reads the loud tone, bar 4 the soft one.
	b := r.Bars
	if b[0].ShortTermLUFS != nil || b[1].ShortTermLUFS == nil || b[3].ShortTermLUFS == nil || math.Abs(*b[1].ShortTermLUFS-*b[3].ShortTermLUFS-6.02) > 0.2 {
		t.Errorf("bar short-term %v %v %v", b[0].ShortTermLUFS, b[1].ShortTermLUFS, b[3].ShortTermLUFS)
	}
	if !strings.Contains(textOf(a), "loudest section: loud") {
		t.Error("text report does not name the loudest section")
	}
}

func TestSilenceAndPeak(t *testing.T) {
	a := analyzeWith(t, gaps, Options{Peaks: 3})
	t.Logf("%s", textOf(a))
	r := a.Report
	if len(r.Activity.Silences) != 1 {
		t.Fatalf("silences %+v, want one in bar 2", r.Activity.Silences)
	}
	s := r.Activity.Silences[0]
	// Bar 2 runs from 2 s to 4 s; the 10 ms release ends within one block.
	if s.Start.Bar != 2 || s.Start.TimeS > 2.03 || s.End.Bar != 3 || s.End.TimeS < 3.99 || s.LengthS < 1.95 {
		t.Errorf("silence %+v, want bar 2", s)
	}
	if len(r.Bars[1].Sounding) != 0 || len(r.Bars[0].Sounding) != 2 {
		t.Errorf("sounding bar 1 %v, bar 2 %v", r.Bars[0].Sounding, r.Bars[1].Sounding)
	}
	if len(r.Peaks) != 3 {
		t.Fatalf("%d peaks, want 3", len(r.Peaks))
	}
	p := r.Peaks[0]
	// The hit starts at tick 1920 (bar 1 beat 3) and peaks within a few ms.
	if p.At.Text != "1.3.1" || p.At.Tick < 1920 || p.At.Tick > 1920+60 || len(p.Tracks) == 0 || p.Tracks[0].ID != "hit" || p.Tracks[0].SharePct < 90 {
		t.Errorf("peak %+v, want the hit at 1.3.1", p)
	}
	if p.TruePeakDBTP < p.SamplePeakDBFS || p.TruePeakDBTP-p.SamplePeakDBFS > 1 {
		t.Errorf("true peak %.1f against sample peak %.1f", p.TruePeakDBTP, p.SamplePeakDBFS)
	}
	for _, q := range r.Peaks[1:] {
		if q.TruePeakDBTP > p.TruePeakDBTP || math.Abs(q.At.TimeS-p.At.TimeS) < peakSpacingS {
			t.Errorf("peak %+v is not below and apart from the first", q)
		}
	}
}

func TestTrackSelection(t *testing.T) {
	a := analyzeWith(t, alternateLow, Options{Tracks: []string{"two", "one"}})
	r := a.Report
	if len(r.Tracks) != 2 || r.Tracks[0].ID != "one" || r.Tracks[1].ID != "two" || strings.Join(r.SelectedTracks, ",") != "one,two" {
		t.Fatalf("tracks %v, selected %v", len(r.Tracks), r.SelectedTracks)
	}
	for _, b := range r.Bars {
		for _, id := range b.Sounding {
			if id != "one" && id != "two" {
				t.Errorf("bar %d lists %s", b.Bar, id)
			}
		}
	}
	score, _ := notation.Parse([]byte(alternateLow))
	_, err := Analyze(score, Options{TempDir: t.TempDir(), Tracks: []string{"one", "bogus"}})
	var unknown *render.UnknownTrackError
	if !errors.As(err, &unknown) || unknown.Name != "bogus" || strings.Join(unknown.Valid, ",") != "one,two,steady,human" {
		t.Fatalf("unknown track error %v", err)
	}
}

func TestPicturesPageLongRanges(t *testing.T) {
	a := analyzeWith(t, alternateLow, Options{})
	files, list, err := a.Pictures(400)
	if err != nil {
		t.Fatal(err)
	}
	// 8 bars at 120 BPM are 94 spectrogram columns each: 4 bars fit in 400.
	var names []string
	for _, p := range list {
		names = append(names, p.File)
		if p.Width > 400 || !bytes.HasPrefix(files[p.File], []byte("\x89PNG")) {
			t.Errorf("%s: %d wide", p.File, p.Width)
		}
	}
	want := "spectrogram.png chroma.png rhythm.png spectrogram-bars-001-004.png chroma-bars-001-004.png spectrogram-bars-005-008.png chroma-bars-005-008.png energy.png"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("pictures %s\nwant %s", got, want)
	}
	if list[0].Height != specRows*2 || !list[0].Scaled || list[3].Scaled || list[3].FromBar != 1 || list[3].ToBar != 4 {
		t.Errorf("pictures %+v", list)
	}
	// Uncapped, one strip each and no pages.
	_, list, err = a.Pictures(0)
	if err != nil || len(list) != 4 || list[0].Scaled {
		t.Errorf("uncapped pictures %+v, %v", list, err)
	}
}

// TestBandFramesWeighEveryInstant: short bursts of a sine read the same band
// energy whether they start on a frame centre or between two, because the
// band frames are spaced so that their squared windows sum to a constant. The
// bursts repeat every 12 frames, so each lands at the same place against the
// frames, as hits on a tempo grid do against grid-tied frames. Frames half a
// window apart read these two placements 0.8 dB apart.
func TestBandFramesWeighEveryInstant(t *testing.T) {
	const rate, n = 48000, 48000 * 8
	const period, length = 12 * bandHop, 3072
	held := stereo{L: make([]float32, n), R: make([]float32, n)}
	for i := range held.L {
		held.L[i] = float32(0.25 * math.Sin(2*math.Pi*55*float64(i)/rate))
		held.R[i] = held.L[i]
	}
	burst := func(offset int) stereo {
		// The same energy as the held sine: a fraction of the time at a
		// power raised by its inverse.
		s := stereo{L: make([]float32, n), R: make([]float32, n)}
		for i := range s.L {
			if ((i-offset)%period+period)%period < length {
				s.L[i] = float32(math.Sqrt(period/length)) * held.L[i]
				s.R[i] = s.L[i]
			}
		}
		return s
	}
	plan := newPlan(chromaFFT)
	bins := binBands(chromaFFT, rate)
	low := bandIndex(t, "low")
	total := func(s stereo) float64 {
		var sum float64
		for _, f := range bandPower(s, plan, bins, newBandLayout([]int{0, n})) {
			sum += f[low]
		}
		return sum
	}
	ref := total(held)
	var got []float64
	for _, offset := range []int{0, bandHop / 2, bandHop / 3} {
		got = append(got, 10*math.Log10(total(burst(offset))/ref))
	}
	// A 64 ms burst spills a little out of 40-80 Hz: about -0.25 dB.
	for i, d := range got {
		if math.Abs(d) > 0.5 || math.Abs(d-got[0]) > 0.05 {
			t.Errorf("bursts at placement %d read %.2f dB against the held sine (first placement %.2f dB)", i, d, got[0])
		}
	}
}

// TestPowFramesSquareMagFrames: the band frames' power is the square of the
// magnitude the other measures use, to rounding.
func TestPowFramesSquareMagFrames(t *testing.T) {
	s := stereo{L: make([]float32, 20000), R: make([]float32, 20000)}
	for i := range s.L {
		s.L[i] = float32(math.Sin(float64(i)*0.031) + 0.3*math.Sin(float64(i)*0.7))
		s.R[i] = float32(-0.5 * math.Sin(float64(i)*0.011))
	}
	plan := newPlan(chromaFFT)
	start := func(i int) int { return i*5000 - 3000 }
	var mags [][]float64
	magFrames(s, plan, 4, start, func(_ int, mag []float64) { mags = append(mags, append([]float64(nil), mag...)) })
	powFrames(s, plan, 4, start, func(i int, pow []float64) {
		for k, p := range pow {
			want := mags[i][k] * mags[i][k]
			if math.Abs(p-want) > 1e-9*math.Max(want, 1e-6) {
				t.Fatalf("frame %d bin %d: power %g, magnitude squared %g", i, k, p, want)
			}
		}
	})
}

// TestNoBands: without band ownership the band fields are absent and the
// other measures are unchanged.
func TestNoBands(t *testing.T) {
	with := analyzeWith(t, levels, Options{})
	without := analyzeWith(t, levels, Options{NoBands: true})
	r := without.Report
	if r.Bands != nil || r.Bars[0].Bands != nil || r.Sections[0].Bands != nil || r.Tracks[0].BandsDB != nil {
		t.Fatalf("band fields remain: %+v", r.Bands)
	}
	data, err := json.Marshal(r)
	if err != nil || bytes.Contains(data, []byte(`"bands`)) {
		t.Errorf("JSON still has band fields: %v", err)
	}
	if *with.Report.Loudness.IntegratedLUFS != *r.Loudness.IntegratedLUFS || with.Report.Bars[1].Sounding[0] != r.Bars[1].Sounding[0] {
		t.Error("other measures changed")
	}
}
