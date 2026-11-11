package scoreaudition

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/cicada/kernel/loudness"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// LoudnessReport is the loudness of the mix over the analyzed range, measured
// with the render's own ITU-R BS.1770-4 meter (kernel/loudness), the one that
// checks a render's loudness target. Values are rounded to 0.1 and floored at
// -120; a value that the method cannot give is null.
type LoudnessReport struct {
	// IntegratedLUFS is null when no 400 ms block is above the -70 LUFS gate.
	IntegratedLUFS   *float64 `json:"integrated_lufs"`
	LoudnessRangeLU  float64  `json:"loudness_range_lu"`
	TruePeakDBTP     float64  `json:"true_peak_dbtp"`
	SamplePeakDBFS   float64  `json:"sample_peak_dbfs"`
	MaxShortTermLUFS *float64 `json:"max_short_term_lufs"` // null under 3 s
	MaxMomentaryLUFS *float64 `json:"max_momentary_lufs"`  // null under 400 ms
	// LoudestSection and QuietestSection name the sections with the highest
	// and lowest integrated loudness; SectionSpreadLU is their difference.
	LoudestSection  string   `json:"loudest_section,omitempty"`
	QuietestSection string   `json:"quietest_section,omitempty"`
	SectionSpreadLU *float64 `json:"section_spread_lu"`
	// ShortTermPerBeatLUFS has one value per beat (a quarter note): the 3 s
	// window that ends where the beat ends. Null while under 3 s of the range
	// precede it.
	ShortTermPerBeatLUFS []*float64 `json:"short_term_per_beat_lufs"`
}

// Position is a moment in the analyzed range. Bar, Beat and Sixteenth are
// one-based song positions (Text is "bar.beat.sixteenth", as a placement's
// @ position); Tick is absolute (960 per quarter note); TimeS is seconds from
// the start of the analyzed range.
type Position struct {
	Text      string  `json:"text"`
	Bar       int     `json:"bar"`
	Beat      int     `json:"beat"`
	Sixteenth int     `json:"sixteenth"`
	Tick      int64   `json:"tick"`
	TimeS     float64 `json:"time_s"`
}

// PeakReport is one of the highest true-peak moments of the mix.
type PeakReport struct {
	Rank           int                `json:"rank"`
	At             Position           `json:"at"`
	TruePeakDBTP   float64            `json:"true_peak_dbtp"`
	SamplePeakDBFS float64            `json:"sample_peak_dbfs"`
	Tracks         []PeakContribution `json:"tracks"`
}

// PeakContribution is one track's part of a peak: its share of the summed
// track-stem energy in the 5 ms around the peak, and its own sample peak
// there. Effect returns are not tracks and are left out.
type PeakContribution struct {
	ID       string  `json:"id"`
	SharePct float64 `json:"share_pct"`
	PeakDBFS float64 `json:"peak_dbfs"`
}

const (
	ticksPerBeat = seq.TicksPerStep * 4
	shortTermS   = 3
	// DefaultPeaks is how many peaks the report lists when Options.Peaks is 0.
	DefaultPeaks = 10
	// peakSpacingS keeps two listed peaks apart, so one hit is one peak.
	peakSpacingS = 0.05
	// peakContextS is the half-width of the window that attributes a peak.
	peakContextS = 0.0025
	// peakCandidates is how many of the highest sample-peak moments get a
	// true-peak measurement, per listed peak.
	peakCandidates = 8
)

func loudnessValue(v float64) *float64 {
	if math.IsNaN(v) {
		return nil
	}
	x := round(math.Max(-120, v), 1)
	return &x
}

func dbFloor(v float64) float64 { return round(math.Max(-120, v), 1) }

// positionAt converts a sample offset in the analyzed range to a position.
func positionAt(clock *project.TempoMap, base int64, sample, rate int) Position {
	tick := clock.TickAtSample(base + int64(sample))
	p := Position{Bar: int(tick/seq.TicksPerBar) + 1, Beat: int(tick%seq.TicksPerBar/ticksPerBeat) + 1,
		Sixteenth: int(tick%ticksPerBeat/seq.TicksPerStep) + 1, Tick: tick, TimeS: round(float64(sample)/float64(rate), 3)}
	p.Text = fmt.Sprintf("%d.%d.%d", p.Bar, p.Beat, p.Sixteenth)
	return p
}

// mixLoudness feeds the mix once through a meter and reads the short-term
// loudness at every bar end, every beat end and every 100 ms (the curve of
// energy.png, NaN before 3 s).
type mixLoudness struct {
	report   *LoudnessReport
	perBar   []*float64
	curve    []float64
	curveHop int
}

func measureMixLoudness(mix stereo, rate int, barEnds, beatEnds []int) (mixLoudness, error) {
	out := mixLoudness{curveHop: rate / 10}
	m, err := loudness.New(rate)
	if err != nil {
		return out, nil // a rate the meter does not support: no loudness block
	}
	n := mix.Len()
	out.perBar = make([]*float64, len(barEnds))
	perBeat := make([]*float64, len(beatEnds))
	short := func(at int) *float64 {
		if at < shortTermS*rate {
			return nil
		}
		return loudnessValue(m.Metrics().ShortTermLUFS)
	}
	bi, ki := 0, 0
	nextCurve := out.curveHop
	pos := 0
	for pos < n {
		next := min(n, nextCurve)
		if bi < len(barEnds) {
			next = min(next, max(barEnds[bi], pos+1))
		}
		if ki < len(beatEnds) {
			next = min(next, max(beatEnds[ki], pos+1))
		}
		if err := m.ProcessBlock(mix.L[pos:next], mix.R[pos:next]); err != nil {
			return out, err
		}
		pos = next
		for bi < len(barEnds) && barEnds[bi] <= pos {
			out.perBar[bi] = short(pos)
			bi++
		}
		for ki < len(beatEnds) && beatEnds[ki] <= pos {
			perBeat[ki] = short(pos)
			ki++
		}
		if pos == nextCurve {
			v := math.NaN()
			if pos >= shortTermS*rate {
				v = math.Max(-120, m.Metrics().ShortTermLUFS)
			}
			out.curve = append(out.curve, v)
			nextCurve += out.curveHop
		}
	}
	for ; bi < len(barEnds); bi++ {
		out.perBar[bi] = short(n)
	}
	for ; ki < len(beatEnds); ki++ {
		perBeat[ki] = short(n)
	}
	res := m.Metrics()
	r := &LoudnessReport{IntegratedLUFS: loudnessValue(res.IntegratedLUFS), LoudnessRangeLU: round(res.LoudnessRange, 1),
		TruePeakDBTP: dbFloor(res.TruePeakDBTP), SamplePeakDBFS: dbFloor(res.SamplePeakDBFS), ShortTermPerBeatLUFS: perBeat}
	if res.IntegratedLUFS == math.Inf(-1) {
		r.IntegratedLUFS = nil
	}
	if n >= shortTermS*rate {
		r.MaxShortTermLUFS = loudnessValue(res.MaxShortTermLUFS)
	}
	if n >= int(math.Round(float64(rate)*0.4)) {
		r.MaxMomentaryLUFS = loudnessValue(res.MaxMomentaryLUFS)
	}
	out.report = r
	return out, nil
}

// sectionLoudness is a fresh meter over one section's samples.
type sectionLoudness struct {
	integrated    *float64
	rangeLU       float64
	truePeak      float64
	stMax, stMin  *float64
	integratedRaw float64
	measured      bool
}

func measureSectionLoudness(m *loudness.Meter, sig stereo, rate int) (sectionLoudness, error) {
	var out sectionLoudness
	if m == nil {
		return out, nil
	}
	m.Reset()
	hop := rate / 10
	stMin := math.Inf(1)
	for pos := 0; pos < sig.Len(); pos += hop {
		end := min(sig.Len(), pos+hop)
		if err := m.ProcessBlock(sig.L[pos:end], sig.R[pos:end]); err != nil {
			return out, err
		}
		if end >= shortTermS*rate && end-pos == hop {
			stMin = math.Min(stMin, m.Metrics().ShortTermLUFS)
		}
	}
	res := m.Metrics()
	out.measured = true
	out.integratedRaw = res.IntegratedLUFS
	if res.IntegratedLUFS != math.Inf(-1) {
		out.integrated = loudnessValue(res.IntegratedLUFS)
	}
	out.rangeLU = round(res.LoudnessRange, 1)
	out.truePeak = dbFloor(res.TruePeakDBTP)
	if sig.Len() >= shortTermS*rate {
		out.stMax = loudnessValue(res.MaxShortTermLUFS)
		out.stMin = loudnessValue(stMin)
	}
	return out, nil
}

// peakCandidate is a moment whose true peak was measured.
type peakCandidate struct {
	sample   int // position of the largest sample
	truePeak float64
	sample1  float64 // sample peak, linear
}

// findPeaks returns the n highest true-peak moments of the mix, highest
// first. The mix is cut into 10 ms blocks; the blocks with the highest sample
// peaks, at least 50 ms apart, are candidates (8 per peak asked for), and each
// candidate's true peak is measured with the meter over the 5 ms around it.
// A true peak exceeds its sample peak by a few dB at most, so a moment outside
// the candidates could rank only if many candidates sit within that margin.
func findPeaks(m *loudness.Meter, mix stereo, rate, n int) ([]peakCandidate, error) {
	if m == nil || n <= 0 || mix.Len() == 0 {
		return nil, nil
	}
	block := max(1, rate/100)
	type blk struct {
		at   int
		peak float32
	}
	var blocks []blk
	for start := 0; start < mix.Len(); start += block {
		b := blk{at: start}
		for i := start; i < min(mix.Len(), start+block); i++ {
			if v := max(float32(math.Abs(float64(mix.L[i]))), float32(math.Abs(float64(mix.R[i])))); v > b.peak {
				b.peak, b.at = v, i
			}
		}
		if b.peak > 0 {
			blocks = append(blocks, b)
		}
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].peak > blocks[j].peak })
	spacing := int(peakSpacingS * float64(rate))
	limit := n * peakCandidates
	var chosen []blk
	for _, b := range blocks {
		if len(chosen) == limit {
			break
		}
		near := false
		for _, c := range chosen {
			if c.at-b.at < spacing && b.at-c.at < spacing {
				near = true
				break
			}
		}
		if !near {
			chosen = append(chosen, b)
		}
	}
	context := int(peakContextS * float64(rate))
	var out []peakCandidate
	for _, c := range chosen {
		// 24 more samples on each side fill the interpolator's taps.
		lo, hi := max(0, c.at-context-24), min(mix.Len(), c.at+context+25)
		m.Reset()
		if err := m.ProcessBlock(mix.L[lo:hi], mix.R[lo:hi]); err != nil {
			return nil, err
		}
		out = append(out, peakCandidate{sample: c.at, truePeak: m.Metrics().TruePeak, sample1: float64(c.peak)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].truePeak != out[j].truePeak {
			return out[i].truePeak > out[j].truePeak
		}
		return out[i].sample < out[j].sample
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// peakTracks attributes a peak to the analyzed tracks: each stem's energy in
// the 5 ms around it, as a share of the stems' sum, listed from the largest,
// for shares of at least 1%, at most five tracks.
func peakTracks(at, rate int, ids []string, stems []stereo, use []bool) []PeakContribution {
	context := int(peakContextS * float64(rate))
	type part struct {
		id     string
		energy float64
		peak   float64
	}
	var parts []part
	var total float64
	for ti, s := range stems {
		if !use[ti] {
			continue
		}
		lo, hi := min(max(0, at-context), s.Len()), min(s.Len(), at+context+1)
		p := part{id: ids[ti]}
		for i := lo; i < hi; i++ {
			l, r := float64(s.L[i]), float64(s.R[i])
			p.energy += l*l + r*r
			p.peak = math.Max(p.peak, math.Max(math.Abs(l), math.Abs(r)))
		}
		total += p.energy
		parts = append(parts, p)
	}
	out := []PeakContribution{}
	if total <= 0 {
		return out
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].energy > parts[j].energy })
	for _, p := range parts {
		share := 100 * p.energy / total
		if share < 1 || len(out) == 5 {
			break
		}
		out = append(out, PeakContribution{ID: p.id, SharePct: round(share, 1), PeakDBFS: dbFloor(db(p.peak))})
	}
	return out
}
