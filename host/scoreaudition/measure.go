package scoreaudition

import (
	"math"
	"sort"

	"m31labs.dev/cicada/kernel/loudness"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// sectionSpan is a section clipped to the analyzed range: absolute ticks
// [lo, hi) and samples [s0, s1) from the range start.
type sectionSpan struct {
	lo, hi int64
	s0, s1 int
}

// mixInput is what Analyze already holds when it adds the energy, band,
// activity and peak measures: no new render and no new stems.
type mixInput struct {
	mix        stereo
	stems      []stereo
	ids        []string
	selected   []bool
	rate       int
	clock      *project.TempoMap
	base       int64
	fromTick   int64
	bars       int
	barSample  func(int) int
	notes      *noteLog
	acts       []barActivity
	played     []bool
	bandFrames [][][numBands]float64 // per track, bandPower frames (nil when silent)
	layout     bandLayout
	sections   []sectionSpan // aligned with Report.Sections
	peaks      int
	barBands   bool
	noBands    bool
}

func (a *Analysis) measureMix(in mixInput) error {
	r := &a.Report
	rate, bars := in.rate, in.bars
	a.mixLen = in.mix.Len()
	for b := 0; b <= bars; b++ {
		a.barSamples = append(a.barSamples, min(in.barSample(b), in.mix.Len()))
	}
	// Tracks that count in shares: chosen and not silent.
	use := make([]bool, len(in.ids))
	for ti := range use {
		use[ti] = in.selected[ti] && ti < len(r.Tracks) && !r.Tracks[ti].Silent
	}

	// Energy over time.
	barEnds := make([]int, bars)
	for b := range barEnds {
		barEnds[b] = a.barSamples[b+1]
	}
	beatEnds := make([]int, bars*4)
	for k := range beatEnds {
		beatEnds[k] = min(in.mix.Len(), max(0, int(in.clock.SampleAtTick(in.fromTick+int64(k+1)*ticksPerBeat)-in.base)))
	}
	ml, err := measureMixLoudness(in.mix, rate, barEnds, beatEnds)
	if err != nil {
		return err
	}
	r.Loudness = ml.report
	a.curve, a.curveHop = ml.curve, ml.curveHop
	var meter *loudness.Meter
	if r.Loudness != nil {
		meter, _ = loudness.New(rate)
	}
	for b := range r.Bars {
		if ml.perBar != nil {
			r.Bars[b].ShortTermLUFS = ml.perBar[b]
		}
		lo, hi := a.barSamples[b], a.barSamples[b+1]
		var peak float64
		for i := lo; i < hi; i++ {
			peak = math.Max(peak, math.Max(math.Abs(float64(in.mix.L[i])), math.Abs(float64(in.mix.R[i]))))
		}
		r.Bars[b].SamplePeakDBFS = dbFloor(db(peak))
	}
	loudest, quietest := -1, -1
	for si, sp := range in.sections {
		sl, err := measureSectionLoudness(meter, in.mix.Slice(sp.s0, sp.s1), rate)
		if err != nil {
			return err
		}
		sec := &r.Sections[si]
		mark := sectionMark{name: sec.Name, from: sp.s0, to: sp.s1, integrated: math.NaN(), fromBar: sec.FromBar}
		if sl.measured {
			sec.IntegratedLUFS, sec.LoudnessRangeLU, sec.TruePeakDBTP = sl.integrated, sl.rangeLU, sl.truePeak
			sec.ShortTermMaxLUFS, sec.ShortTermMinLUFS = sl.stMax, sl.stMin
		}
		if sec.IntegratedLUFS != nil {
			mark.integrated = sl.integratedRaw
			if loudest < 0 || sl.integratedRaw > a.sectionMarks[loudest].integrated {
				loudest = si
			}
			if quietest < 0 || sl.integratedRaw < a.sectionMarks[quietest].integrated {
				quietest = si
			}
		}
		a.sectionMarks = append(a.sectionMarks, mark)
	}
	if loudest >= 0 && r.Loudness != nil {
		r.Loudness.LoudestSection, r.Loudness.QuietestSection = r.Sections[loudest].Name, r.Sections[quietest].Name
		r.Loudness.SectionSpreadLU = loudnessValue(*r.Sections[loudest].IntegratedLUFS - *r.Sections[quietest].IntegratedLUFS)
		for si := range r.Sections {
			if v := r.Sections[si].IntegratedLUFS; v != nil {
				r.Sections[si].VsLoudestLU = loudnessValue(*v - *r.Sections[loudest].IntegratedLUFS)
			}
		}
	}

	// Activity.
	grace := int(tailGraceS * float64(rate))
	for b := range r.Bars {
		r.Bars[b].Sounding = []string{}
	}
	repeat := in.notes.barRepeats(len(in.ids), in.fromTick, bars)
	for ti := range r.Tracks {
		tr := &r.Tracks[ti]
		var starts []bool
		if ti < len(in.acts) {
			starts = in.acts[ti].played
		}
		full, test := barLevels(in.stems[ti], in.barSample, bars, grace, in.played[ti], starts)
		tr.BarLevelDB, tr.Sounding = sounding(full, test)
		if !in.selected[ti] {
			continue
		}
		for b, on := range tr.Sounding {
			if on {
				r.Bars[b].Sounding = append(r.Bars[b].Sounding, tr.ID)
			}
		}
		rp := repeat[ti]
		tr.NoteBars, tr.SameAsPreviousBar, tr.SameAsFourBarsBefore, tr.LongestSameRun = rp.noteBars, rp.previous, rp.fourBefore, rp.longestRun
	}
	r.Activity = &ActivityReport{SoundingThresholdDB: soundingBelowDB, TailGraceS: tailGraceS, SilenceBelowDBFS: silenceBelowDBFS,
		SilenceMinS: silenceMinS, Silences: findSilences(in.mix, rate, in.clock, in.base)}

	// Band ownership, from the per-sixteenth transforms of each stem.
	if !in.noBands {
		a.measureBands(in, use)
	}

	// Peaks.
	n := in.peaks
	if n == 0 {
		n = DefaultPeaks
	}
	r.Peaks = []PeakReport{}
	cands, err := findPeaks(meter, in.mix, rate, n)
	if err != nil {
		return err
	}
	for i, c := range cands {
		r.Peaks = append(r.Peaks, PeakReport{Rank: i + 1, At: positionAt(in.clock, in.base, c.sample, rate),
			TruePeakDBTP: dbFloor(db(c.truePeak)), SamplePeakDBFS: dbFloor(db(c.sample1)),
			Tracks: peakTracks(c.sample, rate, in.ids, in.stems, use)})
	}
	return nil
}

func (a *Analysis) measureBands(in mixInput, use []bool) {
	r := &a.Report
	bars, tracks := in.bars, len(in.ids)
	// Each frame belongs to its bar, and counts in the section that holds
	// its centre. A bar or section is worth its length over the hop in frames.
	frameBar := in.layout.bar
	barFrames := in.layout.weight
	frameSection := make([]int, len(frameBar))
	secFrames := make([]float64, len(in.sections))
	for si, sp := range in.sections {
		secFrames[si] = float64(sp.s1-sp.s0) / bandHop
	}
	for f, centre := range in.layout.centre {
		frameSection[f] = -1
		for si, sp := range in.sections {
			if centre >= sp.s0 && centre < sp.s1 {
				frameSection[f] = si
			}
		}
	}
	// energy[b][band][track], summed over the bar's frames.
	energy := make([][numBands][]float64, bars)
	for b := range energy {
		for k := range energy[b] {
			energy[b][k] = make([]float64, tracks)
		}
	}
	whole := make([][numBands]float64, tracks)
	secEnergy := make([][numBands][]float64, len(in.sections))
	for si := range secEnergy {
		for k := range secEnergy[si] {
			secEnergy[si][k] = make([]float64, tracks)
		}
	}
	for ti, fr := range in.bandFrames {
		for f, bands := range fr {
			b := frameBar[f]
			for k, e := range bands {
				energy[b][k][ti] += e
				whole[ti][k] += e
				if si := frameSection[f]; si >= 0 {
					secEnergy[si][k][ti] += e
				}
			}
		}
	}
	// spanMean is the largest band's per-frame mean over some frames.
	spanMean := func(e [numBands][]float64, frames float64) float64 {
		var top float64
		for k := range e {
			var total float64
			for ti, v := range e[k] {
				if use[ti] {
					total += v
				}
			}
			if frames > 0 {
				top = math.Max(top, total/frames)
			}
		}
		return top
	}
	var loudest [numBands]float64
	for b := range energy {
		for k := range energy[b] {
			var total float64
			for ti, e := range energy[b][k] {
				if use[ti] {
					total += e
				}
			}
			loudest[k] = math.Max(loudest[k], total/barFrames[b])
		}
	}
	var top float64
	for _, v := range loudest {
		top = math.Max(top, v)
	}
	report := &BandsReport{Names: bandNames[:], EdgesHz: bandEdgesHz[:], CollisionSharePct: collisionSharePct,
		QuietBelowBandDB: quietBandDB, QuietBelowSpanDB: quietSpanDB, QuietBelowRangeDB: quietRangeDB}
	pairs := make([]map[[2]string]int, numBands)
	for k := range bandNames {
		report.Collisions = append(report.Collisions, BandCollisions{Band: bandNames[k], Bars: []int{}, Pairs: []PairCount{}})
		pairs[k] = map[[2]string]int{}
	}
	index := map[string]int{}
	for ti, id := range in.ids {
		index[id] = ti
	}
	for b := range r.Bars {
		r.Bars[b].Bands = make([]BandOwner, numBands)
		span := spanMean(energy[b], barFrames[b])
		for k := 0; k < numBands; k++ {
			o := owner(k, energy[b][k], in.ids, use, barFrames[b], chromaFFT, loudest[k], span, top)
			r.Bars[b].Bands[k] = o
			if o.Collision {
				report.Collisions[k].Bars = append(report.Collisions[k].Bars, r.Bars[b].Bar)
				pair := [2]string{o.Owner, o.Second}
				if index[pair[0]] > index[pair[1]] {
					pair[0], pair[1] = pair[1], pair[0]
				}
				pairs[k][pair]++
			}
		}
	}
	for k := range pairs {
		for pair, count := range pairs[k] {
			report.Collisions[k].Pairs = append(report.Collisions[k].Pairs, PairCount{Tracks: pair, Bars: count})
		}
		ps := report.Collisions[k].Pairs
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].Bars != ps[j].Bars {
				return ps[i].Bars > ps[j].Bars
			}
			if ps[i].Tracks[0] != ps[j].Tracks[0] {
				return index[ps[i].Tracks[0]] < index[ps[j].Tracks[0]]
			}
			return index[ps[i].Tracks[1]] < index[ps[j].Tracks[1]]
		})
	}
	r.Bands = report
	for si, sp := range in.sections {
		sec := &r.Sections[si]
		sec.Bands = make([]SectionBand, numBands)
		span := spanMean(secEnergy[si], secFrames[si])
		for k := 0; k < numBands; k++ {
			sb := SectionBand{BandOwner: owner(k, secEnergy[si][k], in.ids, use, secFrames[si], chromaFFT, loudest[k], span, top)}
			for b := range r.Bars {
				start := in.fromTick + int64(b)*seq.TicksPerBar
				if start >= sp.lo && start < sp.hi && r.Bars[b].Bands[k].Collision {
					sb.CollisionBars++
				}
			}
			sec.Bands[k] = sb
		}
	}
	totalFrames := math.Max(1, float64(in.mix.Len())/bandHop)
	for ti := range r.Tracks {
		tr := &r.Tracks[ti]
		tr.BandsDB = make([]float64, numBands)
		if in.barBands {
			tr.BarBandsDB = make([][]float64, bars)
		}
		tr.SectionBandsDB = make([][]float64, len(in.sections))
		for k := 0; k < numBands; k++ {
			tr.BandsDB[k] = round(bandLevelDB(whole[ti][k], totalFrames, chromaFFT), 1)
		}
		for b := range tr.BarBandsDB {
			tr.BarBandsDB[b] = make([]float64, numBands)
			for k := 0; k < numBands; k++ {
				tr.BarBandsDB[b][k] = round(bandLevelDB(energy[b][k][ti], barFrames[b], chromaFFT), 1)
			}
		}
		for si := range tr.SectionBandsDB {
			tr.SectionBandsDB[si] = make([]float64, numBands)
			for k := 0; k < numBands; k++ {
				tr.SectionBandsDB[si][k] = round(bandLevelDB(secEnergy[si][k][ti], secFrames[si], chromaFFT), 1)
			}
		}
	}
}
