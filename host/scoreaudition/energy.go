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
	IntegratedLUFS *float64 `json:"integrated_lufs"`
	// LoudnessRangeLU is null when the range is under 3 s: no short-term
	// window fits.
	LoudnessRangeLU  *float64 `json:"loudness_range_lu"`
	TruePeakDBTP     float64  `json:"true_peak_dbtp"`
	SamplePeakDBFS   float64  `json:"sample_peak_dbfs"`
	MaxShortTermLUFS *float64 `json:"max_short_term_lufs"` // null under 3 s
	MaxMomentaryLUFS *float64 `json:"max_momentary_lufs"`  // null under 400 ms
	// LoudestSection and QuietestSection name the sections with the highest
	// and lowest integrated loudness, and the indexes (0-based into sections[])
	// identify them, since a song can play a section twice; SectionSpreadLU is
	// their difference.
	LoudestSection       string   `json:"loudest_section,omitempty"`
	LoudestSectionIndex  *int     `json:"loudest_section_index,omitempty"`
	QuietestSection      string   `json:"quietest_section,omitempty"`
	QuietestSectionIndex *int     `json:"quietest_section_index,omitempty"`
	SectionSpreadLU      *float64 `json:"section_spread_lu"`
	// ShortTermPerBarLUFS has one value per analyzed bar: the 3 s window that
	// ends at the bar line. Null while under 3 s of the range precede it.
	ShortTermPerBarLUFS []*float64 `json:"short_term_per_bar_lufs"`
	// Detail (DropDetail removes it): one value per beat of the song's meter
	// (a quarter note in 4/4), the 3 s window that ends where the beat ends.
	// Null while under 3 s of the range precede it.
	ShortTermPerBeatLUFS []*float64 `json:"short_term_per_beat_lufs,omitempty"`
}

// Position is a moment in the analyzed range. Bar, Beat and Sixteenth are
// one-based song positions on the song's meter map, the sixteenth counted
// within its beat (Text is "bar.beat.sixteenth", as a placement's @
// position); Tick is absolute (960 per quarter note); TimeS is seconds from
// the start of the analyzed range.
type Position struct {
	Text      string  `json:"text"`
	Bar       int     `json:"bar"`
	Beat      int     `json:"beat"`
	Sixteenth int     `json:"sixteenth"`
	Tick      int64   `json:"tick"`
	TimeS     float64 `json:"time_s"`
}

// PeakReport is one of the highest true-peak moments of the mix, and how the
// signal entering the master chain at that instant splits among the stems.
type PeakReport struct {
	Rank           int      `json:"rank"`
	At             Position `json:"at"`
	TruePeakDBTP   float64  `json:"true_peak_dbtp"`
	SamplePeakDBFS float64  `json:"sample_peak_dbfs"`
	// Channel is the channel of the mix's largest sample in the peak.
	Channel string `json:"channel"`
	// PreMasterSample is the signed sum, at that sample and channel, of every
	// track stem and both effect returns: the signal entering the master
	// chain (full scale is 1). The master chain (bus compressor, sidechain,
	// EQ, limiter) can reshape it before it becomes the mix's peak.
	PreMasterSample float64 `json:"pre_master_sample"`
	// PreMasterParts splits PreMasterSample: each part's own sample there as a
	// signed percentage of it. Parts and PreMasterRestPct add up to 100; a
	// negative part lowers the peak. Listed: parts of at least 2% in size, at
	// most five, largest first.
	PreMasterParts   []PeakPart `json:"pre_master_parts"`
	PreMasterRestPct float64    `json:"pre_master_rest_pct"`
}

// PeakPart is one stem's part of a peak's pre-master sample.
type PeakPart struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // track or return
	// Sample is the stem's signed sample at the peak (full scale is 1).
	Sample float64 `json:"sample"`
	// SignedSharePct is Sample over the pre-master sample, in percent, with
	// the sign that says whether the part raises or lowers the peak.
	SignedSharePct float64 `json:"signed_share_pct"`
}

const (
	shortTermS = 3
	// DefaultPeaks is how many peaks the report lists when Options.Peaks is 0.
	DefaultPeaks = 3
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

// positionAt converts a sample offset in the analyzed range to a position on
// the song's meter map.
func positionAt(clock *project.TempoMap, meter seq.MeterMap, base int64, sample, rate int) Position {
	tick := clock.TickAtSample(base + int64(sample))
	bar, beat, step, _ := meter.Position(max(tick, 0))
	p := Position{Bar: int(bar), Beat: int(beat), Sixteenth: int(step), Tick: tick, TimeS: round(float64(sample)/float64(rate), 3)}
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
	r := &LoudnessReport{IntegratedLUFS: loudnessValue(res.IntegratedLUFS),
		TruePeakDBTP: dbFloor(res.TruePeakDBTP), SamplePeakDBFS: dbFloor(res.SamplePeakDBFS), ShortTermPerBeatLUFS: perBeat}
	if res.IntegratedLUFS == math.Inf(-1) {
		r.IntegratedLUFS = nil
	}
	if n >= shortTermS*rate {
		r.MaxShortTermLUFS = loudnessValue(res.MaxShortTermLUFS)
		r.LoudnessRangeLU = loudnessValue(res.LoudnessRange)
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
	rangeLU       *float64 // null under 3 s
	truePeak      float64
	integratedRaw float64
	measured      bool
}

func measureSectionLoudness(m *loudness.Meter, sig stereo, rate int) (sectionLoudness, error) {
	var out sectionLoudness
	if m == nil {
		return out, nil
	}
	m.Reset()
	if err := m.ProcessBlock(sig.L, sig.R); err != nil {
		return out, err
	}
	res := m.Metrics()
	out.measured = true
	out.integratedRaw = res.IntegratedLUFS
	if res.IntegratedLUFS != math.Inf(-1) {
		out.integrated = loudnessValue(res.IntegratedLUFS)
	}
	out.truePeak = dbFloor(res.TruePeakDBTP)
	if sig.Len() >= shortTermS*rate {
		out.rangeLU = loudnessValue(res.LoudnessRange)
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

// stemSample is one stem's signed sample at a peak.
type stemSample struct {
	id, kind string
	v        float64
}

// maxPeakParts is how many parts a peak lists, and minPeakPartPct the
// smallest share listed; the rest is summed.
const (
	maxPeakParts   = 5
	minPeakPartPct = 2
)

// attributePeak splits a pre-master sample among its parts: each part's own
// sample as a signed percentage of total, so a part that pulls the other way
// is negative. Parts under 2% in size, and parts past the fifth largest, go
// into the rest; the listed parts and the rest add up to 100. A total under
// 1e-6 cannot be split: no parts and a rest of 0.
func attributePeak(total float64, values []stemSample) ([]PeakPart, float64) {
	out := []PeakPart{}
	if math.Abs(total) < 1e-6 {
		return out, 0
	}
	sorted := append([]stemSample(nil), values...)
	sort.SliceStable(sorted, func(i, j int) bool { return math.Abs(sorted[i].v) > math.Abs(sorted[j].v) })
	listed := 0.0
	for _, s := range sorted {
		share := 100 * s.v / total
		if math.Abs(share) < minPeakPartPct || len(out) == maxPeakParts {
			break
		}
		out = append(out, PeakPart{ID: s.id, Kind: s.kind, Sample: plusZero(round(s.v, 4)), SignedSharePct: plusZero(round(share, 1))})
		listed += share
	}
	return out, plusZero(round(100-listed, 1))
}

// plusZero turns a rounded -0 into 0, so JSON never prints -0.
func plusZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// peakChannel returns the channel (0 left, 1 right) of the larger mix sample
// at a frame.
func peakChannel(mix stereo, at int) int {
	if at < mix.Len() && math.Abs(float64(mix.R[at])) > math.Abs(float64(mix.L[at])) {
		return 1
	}
	return 0
}

// channelName names a channel index for the report.
func channelName(ch int) string {
	if ch == 1 {
		return "right"
	}
	return "left"
}

// sampleAt is one channel of a stereo stem at a frame, 0 past its end.
func sampleAt(s stereo, at, ch int) float64 {
	if at < 0 || at >= s.Len() {
		return 0
	}
	if ch == 1 {
		return float64(s.R[at])
	}
	return float64(s.L[at])
}
