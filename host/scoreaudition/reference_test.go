package scoreaudition

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/loudness"
)

// Tests against reference values computed outside this package's code.

func sine(n int, freq, amp, phase float64) stereo {
	s := stereo{L: make([]float32, n), R: make([]float32, n)}
	for i := range s.L {
		s.L[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/48000+phase))
		s.R[i] = s.L[i]
	}
	return s
}

// TestBandEdgesSplitSines: a bin belongs to the band that holds its centre
// frequency, so a sine on an edge splits between the two bands by where the
// edge falls between the 5.86 Hz bins. The splits below were computed with
// numpy (an 8,192-point symmetric Hann window, numpy.fft.rfft, bins assigned
// by centre frequency); the manual states them. A sine at a band's geometric
// centre stays in its band.
func TestBandEdgesSplitSines(t *testing.T) {
	plan := newPlan(chromaFFT)
	bins := binBands(chromaFFT, 48000)
	split := func(freq float64) [numBands]float64 {
		var power [numBands]float64
		// One frame well inside a long sine: no edge of the signal in view.
		powFrames(sine(4*chromaFFT, freq, 1, 0.3), plan, 1, func(int) int { return chromaFFT }, func(_ int, pow []float64) {
			for k, b := range bins {
				if b >= 0 {
					power[b] += pow[k]
				}
			}
		})
		var total float64
		for _, p := range power {
			total += p
		}
		for b := range power {
			power[b] /= total
		}
		return power
	}
	cases := []struct {
		edge       float64
		below      int
		belowShare float64 // numpy, percent
	}{
		{40, 0, 26.6}, {80, 1, 38.5}, {160, 2, 64.4}, {400, 3, 67.2}, {1600, 4, 79.8}, {5000, 5, 62.5},
	}
	for _, c := range cases {
		got := split(c.edge)
		if math.Abs(100*got[c.below]-c.belowShare) > 0.5 || math.Abs(100*(got[c.below]+got[c.below+1])-100) > 0.1 {
			t.Errorf("%g Hz: %s %.1f%%, %s %.1f%%; want %.1f%% below the edge and nothing elsewhere",
				c.edge, bandNames[c.below], 100*got[c.below], bandNames[c.below+1], 100*got[c.below+1], c.belowShare)
		}
	}
	for b := range numBands {
		centre := math.Sqrt(bandEdgesHz[b] * bandEdgesHz[b+1])
		if got := split(centre); got[b] < 0.999 {
			t.Errorf("%.1f Hz: %.2f%% in %s, want over 99.9%%", centre, 100*got[b], bandNames[b])
		}
	}
}

// TestBandLevelFullScaleSine: a full-scale sine's RMS is -3.01 dB, and the
// band levels of a held sine sum to it through the bar-cut frames.
func TestBandLevelFullScaleSine(t *testing.T) {
	const n = 48000 * 2
	plan := newPlan(chromaFFT)
	bins := binBands(chromaFFT, 48000)
	layout := newBandLayout([]int{0, n})
	for _, freq := range []float64{56.6, 800, 10000} {
		var total float64
		for _, f := range bandPower(sine(n, freq, 1, 0), plan, bins, layout) {
			for _, p := range f {
				total += p
			}
		}
		if got := bandLevelDB(total, layout.weight[0], chromaFFT); math.Abs(got+3.01) > 0.1 {
			t.Errorf("%g Hz full-scale sine: bands sum to %.2f dB, want -3.01", freq, got)
		}
	}
}

// stepSignal is a 1 kHz sine at level a for the first part, then level b.
func stepSignal(first, second float64, a, b float64) stereo {
	n1, n2 := int(first*48000), int(second*48000)
	s := sine(n1+n2, 1000, 1, 0)
	for i := range s.L {
		g := a
		if i >= n1 {
			g = b
		}
		s.L[i] *= float32(g)
		s.R[i] = s.L[i]
	}
	return s
}

// TestLoudnessRangeTwoLevels: EBU Tech 3342 test 1, a 1 kHz sine at -20 dBFS
// for 20 s then -30 dBFS for 20 s, has a loudness range of 10 LU (the
// standard's tolerance is 1 LU). The whole range and a section read it.
// Under 3 s there is no short-term window and the range is null.
func TestLoudnessRangeTwoLevels(t *testing.T) {
	sig := stepSignal(20, 20, math.Pow(10, -20.0/20), math.Pow(10, -30.0/20))
	ml, err := measureMixLoudness(sig, 48000, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ml.report.LoudnessRangeLU == nil || math.Abs(*ml.report.LoudnessRangeLU-10) > 0.2 {
		t.Errorf("whole range: loudness range %v, want 10 LU", ml.report.LoudnessRangeLU)
	}
	meter, _ := loudness.New(48000)
	sl, err := measureSectionLoudness(meter, sig, 48000)
	if err != nil || sl.rangeLU == nil || math.Abs(*sl.rangeLU-10) > 0.2 {
		t.Errorf("section: loudness range %v, %v, want 10 LU", sl.rangeLU, err)
	}
	short := sig.Slice(0, 48000*29/10)
	ml, _ = measureMixLoudness(short, 48000, nil, nil)
	sl, _ = measureSectionLoudness(meter, short, 48000)
	if ml.report.LoudnessRangeLU != nil || sl.rangeLU != nil || ml.report.IntegratedLUFS == nil {
		t.Errorf("2.9 s: loudness range %v and %v, want null (integrated %v)", ml.report.LoudnessRangeLU, sl.rangeLU, ml.report.IntegratedLUFS)
	}
}

// TestSectionTruePeakInterSample: a 12 kHz sine (a quarter of the rate) at
// 45 degrees of phase has every sample at amplitude times 0.7071 (-3.01 dB),
// but its true peak is the amplitude. A section of it reads the true peak.
func TestSectionTruePeakInterSample(t *testing.T) {
	sig := sine(48000, 12000, 0.5, math.Pi/4)
	meter, _ := loudness.New(48000)
	sl, err := measureSectionLoudness(meter, sig, 48000)
	if err != nil {
		t.Fatal(err)
	}
	// 20*log10(0.5) = -6.02 dBTP; the samples peak at -9.03 dBFS.
	if math.Abs(sl.truePeak+6.02) > 0.25 {
		t.Errorf("true peak %.2f dBTP, want -6.02 (samples peak at -9.03)", sl.truePeak)
	}
}

// TestShortTermPerBeatStep: silence for 4 s, then a steady sine. The 3 s
// window that ends at time t holds (t-4)/3 of the sine's power, so the
// short-term loudness there is the steady value plus 10*log10((t-4)/3).
// Beats are 0.5 s apart (120 BPM); none is given before 3 s.
func TestShortTermPerBeatStep(t *testing.T) {
	sig := stepSignal(4, 6, 0, 0.1)
	var beats []int
	for k := 1; k <= 20; k++ {
		beats = append(beats, k*24000)
	}
	ml, err := measureMixLoudness(sig, 48000, nil, beats)
	if err != nil {
		t.Fatal(err)
	}
	got := ml.report.ShortTermPerBeatLUFS
	for k := 0; k < 5; k++ { // beats end at 0.5 to 2.5 s
		if got[k] != nil {
			t.Errorf("beat %d ends at %.1f s: %v, want null", k+1, float64(k+1)/2, *got[k])
		}
	}
	steady := *got[19]                                                                  // 10 s: the window holds only the sine
	for k, frac := range map[int]float64{9: 1.0 / 3, 10: 1.5 / 3, 12: 2.5 / 3, 13: 1} { // 5, 5.5, 6.5 and 7 s
		want := steady + 10*math.Log10(frac)
		if got[k] == nil || math.Abs(*got[k]-want) > 0.15 {
			t.Errorf("beat ending at %.1f s: %v, want %.2f", float64(k+1)/2, got[k], want)
		}
	}
}

// TestPeakPartsSignedShares: the reviewer's cases, read at the frame where a
// transient (0.9) crests. A held 45 Hz sub at its crest (0.7): the pre-master
// sample is 1.6 and the sub supplies 43.75% of it (energy shares said 78%). A
// sub at -0.35 lowers that sample: -63.6%, and the transient +163.6%.
func TestPeakPartsSignedShares(t *testing.T) {
	const n, at = 4800, 2400
	transient := stereo{L: make([]float32, n), R: make([]float32, n)}
	for i := at; i < n; i++ {
		x := float64(i - at)
		transient.L[i] = float32(0.9 * math.Exp(-x/48000/0.004) * math.Cos(2*math.Pi*3000*x/48000))
		transient.R[i] = transient.L[i]
	}
	meter, _ := loudness.New(48000)
	for _, c := range []struct {
		name      string
		phase     float64
		sub, rest float64
	}{
		{"sub at its crest", 0, 43.75, 0},
		{"sub against the transient", 2 * math.Pi / 3, -63.64, 0},
	} {
		sub := sine(n, 45, 0.7, math.Pi/2-2*math.Pi*45*at/48000+c.phase) // 0.7*cos(phase) at frame at
		mix := stereo{L: make([]float32, n), R: make([]float32, n)}
		for i := range mix.L {
			mix.L[i] = sub.L[i] + transient.L[i]
			mix.R[i] = mix.L[i]
		}
		if c.phase == 0 {
			// Both crest together: the mix's peak is that frame.
			peaks, err := findPeaks(meter, mix, 48000, 1)
			if err != nil || len(peaks) != 1 || peaks[0].sample != at {
				t.Fatalf("%s: peaks %+v, %v; want the transient's first sample %d", c.name, peaks, err, at)
			}
		}
		ch := peakChannel(mix, at)
		values := []stemSample{{"sub", "track", sampleAt(sub, at, ch)}, {"hit", "track", sampleAt(transient, at, ch)}}
		parts, rest := attributePeak(values[0].v+values[1].v, values)
		shares := map[string]float64{}
		for _, p := range parts {
			shares[p.ID] = p.SignedSharePct
		}
		if math.Abs(shares["sub"]-c.sub) > 0.1 || math.Abs(shares["hit"]-(100-c.sub)) > 0.1 || rest != c.rest {
			t.Errorf("%s: parts %+v rest %.1f, want sub %.2f%%, hit %.2f%%", c.name, parts, rest, c.sub, 100-c.sub)
		}
	}
	// Parts past the fifth and parts under 1% go into the rest, with whatever
	// the listed stems do not hold (here 0.5% of a total of 1): 4 + 0.5 + 0.5.
	values := []stemSample{{"a", "track", 0.5}, {"b", "track", 0.2}, {"c", "track", 0.1}, {"d", "return", 0.1}, {"e", "track", 0.05}, {"f", "track", 0.04}, {"g", "track", 0.005}}
	parts, rest := attributePeak(1, values)
	if len(parts) != 5 || parts[3].Kind != "return" || math.Abs(rest-5) > 0.01 {
		t.Errorf("parts %+v rest %.2f, want five parts and a rest of 5%%", parts, rest)
	}
	// Three equal parts print 33.3 each: the rest takes the last 0.1, so the
	// printed row adds up to 100.0.
	parts, rest = attributePeak(0.3, []stemSample{{"a", "track", 0.1}, {"b", "track", 0.1}, {"c", "return", 0.1}})
	if sum := parts[0].SignedSharePct + parts[1].SignedSharePct + parts[2].SignedSharePct + rest; len(parts) != 3 || rest != 0.1 || math.Abs(sum-100) > 1e-9 {
		t.Errorf("equal parts %+v rest %v add to %v, want 100.0", parts, rest, sum)
	}
}

// TestDetailFields: DropDetail leaves the summaries and removes the per-bar
// and per-beat matrices; with them, the shapes follow the bars and bands.
func TestDetailFields(t *testing.T) {
	a := analyzeWith(t, levels, Options{})
	r := a.Report
	bars := len(r.Bars)
	tr := r.Tracks[0]
	if len(tr.BarBandsDB) != bars || len(tr.BarBandsDB[0]) != numBands || len(tr.SectionBandsDB) != len(r.Sections) ||
		len(r.Bars[0].Bands) != numBands || len(tr.Sounding) != bars || len(r.Loudness.ShortTermPerBeatLUFS) != 4*bars {
		t.Fatalf("detail shapes: %d bars, track %+v", bars, tr)
	}
	r.DropDetail()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"bar_bands_db"`, `"section_bands_db"`, `"bar_level_db"`, `"sounding":`, `"short_term_per_beat_lufs"`,
		`"bands":[{"band"`, `"bands_db"`, `"band_collision_bars"`} {
		if bytes.Contains(data, []byte(key)) {
			t.Errorf("summary JSON has %s", key)
		}
	}
	for _, c := range r.Bands.Collisions {
		if c.Bars != nil {
			t.Errorf("summary keeps the %s collision bars", c.Band)
		}
	}
	for i, b := range r.Bars {
		if b.SamplePeakDBFS != nil {
			t.Errorf("summary keeps bar %d's sample peak", i+1)
		}
	}
	for _, s := range r.Sections {
		if s.TruePeakDBTP != nil || s.IntegratedLUFS == nil {
			t.Errorf("summary section %s: true peak %v, integrated %v", s.Name, s.TruePeakDBTP, s.IntegratedLUFS)
		}
	}
	for _, key := range []string{`"sounding_spans"`, `"band_owners"`, `"short_term_per_bar_lufs"`, `"collisions"`, `"pairs"`, `"loudest_section_index"`} {
		if !bytes.Contains(data, []byte(key)) {
			t.Errorf("summary JSON lacks %s", key)
		}
	}
}
