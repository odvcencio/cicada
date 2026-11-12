package scoreaudition

import (
	"math"
	"path/filepath"
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
	meter      seq.MeterMap // the song's bar and beat lines
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
	noBands    bool
	// stemDir holds the render's stem files; partial is true when only the
	// chosen tracks' stems were written, with the returns and the buses.
	stemDir string
	partial bool
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
	// Beat ends follow each bar's meter: a beat is one denominator note.
	firstBar, _ := in.meter.BarAt(in.fromTick)
	var beatEnds []int
	for b := range int64(bars) {
		start, beat := in.meter.BarStart(firstBar+b), in.meter.BeatTicks(firstBar+b)
		for k := range in.meter.BeatsInBar(firstBar + b) {
			beatEnds = append(beatEnds, min(in.mix.Len(), max(0, int(in.clock.SampleAtTick(start+(k+1)*beat)-in.base))))
		}
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
	if r.Loudness != nil {
		r.Loudness.ShortTermPerBarLUFS = ml.perBar
	}
	for b := range r.Bars {
		lo, hi := a.barSamples[b], a.barSamples[b+1]
		var peak float64
		for i := lo; i < hi; i++ {
			peak = math.Max(peak, math.Max(math.Abs(float64(in.mix.L[i])), math.Abs(float64(in.mix.R[i]))))
		}
		v := dbFloor(db(peak))
		r.Bars[b].SamplePeakDBFS = &v
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
			tp := sl.truePeak
			sec.IntegratedLUFS, sec.LoudnessRangeLU, sec.TruePeakDBTP = sl.integrated, sl.rangeLU, &tp
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
		r.Loudness.LoudestSectionIndex, r.Loudness.QuietestSectionIndex = &loudest, &quietest
		r.Loudness.SectionSpreadLU = loudnessValue(*r.Sections[loudest].IntegratedLUFS - *r.Sections[quietest].IntegratedLUFS)
		for si := range r.Sections {
			if v := r.Sections[si].IntegratedLUFS; v != nil {
				r.Sections[si].VsLoudestLU = loudnessValue(*v - *r.Sections[loudest].IntegratedLUFS)
			}
		}
	}

	// Activity.
	grace := int(tailGraceS * float64(rate))
	barTracks := make([][]string, len(r.Bars))
	for b := range barTracks {
		barTracks[b] = []string{}
		r.Bars[b].Sounding = &barTracks[b]
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
		tr.SoundingSpans = spansOf(tr.Sounding, r.Score.FromBar)
		if !in.selected[ti] {
			continue
		}
		for b, on := range tr.Sounding {
			if on {
				barTracks[b] = append(barTracks[b], tr.ID)
			}
		}
		rp := repeat[ti]
		tr.NoteBars, tr.SameAsPreviousBar, tr.SameAsFourBarsBefore, tr.LongestSameRun = rp.noteBars, rp.previous, rp.fourBefore, rp.longestRun
	}
	r.Activity = &ActivityReport{SoundingThresholdDB: soundingBelowDB, TailGraceS: tailGraceS, SilenceBelowDBFS: silenceBelowDBFS,
		SilenceMinS: silenceMinS, Silences: findSilences(in.mix, rate, in.clock, in.meter, in.base)}

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
	frames := make([]int, len(cands))
	for i, c := range cands {
		frames[i] = c.sample
	}
	// The returns, and with a track choice the two buses, are read only at the
	// peak frames: they are not held in memory.
	read := func(name string) ([][2]float64, error) {
		if len(frames) == 0 {
			return nil, nil
		}
		return readFloatWAVFrames(filepath.Join(in.stemDir, name), frames)
	}
	returnA, err := read("return-a.wav")
	if err != nil {
		return err
	}
	returnB, err := read("return-b.wav")
	if err != nil {
		return err
	}
	var music, sfx [][2]float64
	if in.partial {
		if music, err = read("music.wav"); err != nil {
			return err
		}
		if sfx, err = read("sfx.wav"); err != nil {
			return err
		}
	}
	for i, c := range cands {
		ch := peakChannel(in.mix, c.sample)
		var values []stemSample
		var total float64
		for ti, s := range in.stems {
			if !in.selected[ti] {
				continue
			}
			v := sampleAt(s, c.sample, ch)
			values = append(values, stemSample{id: in.ids[ti], kind: "track", v: v})
			total += v
		}
		values = append(values, stemSample{id: "return-a", kind: "return", v: returnA[i][ch]}, stemSample{id: "return-b", kind: "return", v: returnB[i][ch]})
		total += returnA[i][ch] + returnB[i][ch]
		if in.partial {
			// The buses sum every track and return, chosen or not.
			total = music[i][ch] + sfx[i][ch]
		}
		parts, rest := attributePeak(total, values)
		r.Peaks = append(r.Peaks, PeakReport{Rank: i + 1, At: positionAt(in.clock, in.meter, in.base, c.sample, rate),
			TruePeakDBTP: dbFloor(db(c.truePeak)), SamplePeakDBFS: dbFloor(db(c.sample1)), Channel: channelName(ch),
			PreMasterSample: plusZero(round(total, 4)), PreMasterParts: parts, PreMasterRestPct: rest})
	}
	return nil
}

// spansOf turns per-bar flags into runs of consecutive bars, one-based from
// firstBar.
func spansOf(on []bool, firstBar int) []BarSpan {
	out := []BarSpan{}
	for b := 0; b < len(on); b++ {
		if !on[b] {
			continue
		}
		end := b
		for end+1 < len(on) && on[end+1] {
			end++
		}
		out = append(out, BarSpan{FromBar: firstBar + b, ToBar: firstBar + end})
		b = end
	}
	return out
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
		report.Collisions = append(report.Collisions, BandCollisions{Band: bandNames[k], Bars: &[]int{}, Pairs: []PairCount{}})
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
				*report.Collisions[k].Bars = append(*report.Collisions[k].Bars, r.Bars[b].Bar)
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
			report.Collisions[k].Pairs = append(report.Collisions[k].Pairs, PairCount{Tracks: pair, BarCount: count})
		}
		ps := report.Collisions[k].Pairs
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].BarCount != ps[j].BarCount {
				return ps[i].BarCount > ps[j].BarCount
			}
			if ps[i].Tracks[0] != ps[j].Tracks[0] {
				return index[ps[i].Tracks[0]] < index[ps[j].Tracks[0]]
			}
			return index[ps[i].Tracks[1]] < index[ps[j].Tracks[1]]
		})
	}
	r.Bands = report
	firstBar, _ := in.meter.BarAt(in.fromTick)
	for si, sp := range in.sections {
		sec := &r.Sections[si]
		sec.Bands = make([]SectionBand, numBands)
		sec.BandOwners, sec.BandCollisionBars = make([]*string, numBands), make([]int, numBands)
		span := spanMean(secEnergy[si], secFrames[si])
		for k := 0; k < numBands; k++ {
			sb := SectionBand{BandOwner: owner(k, secEnergy[si][k], in.ids, use, secFrames[si], chromaFFT, loudest[k], span, top)}
			for b := range r.Bars {
				start := in.meter.BarStart(firstBar + int64(b))
				if start >= sp.lo && start < sp.hi && r.Bars[b].Bands[k].Collision {
					sb.CollisionBars++
				}
			}
			sec.Bands[k] = sb
			sec.BandCollisionBars[k] = sb.CollisionBars
			if !sb.Quiet {
				name := sb.Owner
				sec.BandOwners[k] = &name
			}
		}
	}
	totalFrames := math.Max(1, float64(in.mix.Len())/bandHop)
	for ti := range r.Tracks {
		tr := &r.Tracks[ti]
		tr.BandsDB = make([]float64, numBands)
		tr.BarBandsDB = make([][]float64, bars)
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
