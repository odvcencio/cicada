package scoreaudition

import (
	"math"
	"slices"
	"sort"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

const (
	// soundingBelowDB: a track sounds in a bar when its RMS there is at least
	// this many dB re its own loudest bar (and above -80 dBFS).
	soundingBelowDB = -30.0
	// tailGraceS: for a track that plays notes, a bar with no note start is
	// measured after this much, so a release tail from the bar before does not
	// count as sounding.
	tailGraceS = 0.5
	// silenceBelowDBFS and silenceMinS define a silence in the mix: 10 ms
	// blocks under -60 dBFS RMS, for at least 250 ms.
	silenceBelowDBFS = -60.0
	silenceMinS      = 0.25
)

// ActivityReport states the activity rules and lists the mix's silences.
// Each track's per-bar levels and sounding flags are in tracks[].
type ActivityReport struct {
	SoundingThresholdDB float64   `json:"sounding_threshold_db"`
	TailGraceS          float64   `json:"tail_grace_s"`
	SilenceBelowDBFS    float64   `json:"silence_below_dbfs"`
	SilenceMinS         float64   `json:"silence_min_s"`
	Silences            []Silence `json:"silences"`
}

// Silence is a stretch of the mix under the silence level.
type Silence struct {
	Start   Position `json:"start"`
	End     Position `json:"end"`
	LengthS float64  `json:"length_s"`
}

// barLevels returns, per bar, the track's RMS level in dBFS over the whole bar
// and the level used for the sounding test.
func barLevels(s stereo, barSample func(int) int, bars, grace int, hasNotes bool, noteStarts []bool) (full, test []float64) {
	full, test = make([]float64, bars), make([]float64, bars)
	for b := 0; b < bars; b++ {
		lo, hi := min(barSample(b), s.Len()), min(barSample(b+1), s.Len())
		full[b] = rmsDB(s.Slice(lo, hi))
		test[b] = full[b]
		if hasNotes && (b >= len(noteStarts) || !noteStarts[b]) {
			test[b] = rmsDB(s.Slice(min(lo+grace, hi), hi))
		}
	}
	return full, test
}

// sounding reads the activity of one track from its bar levels.
func sounding(full, test []float64) (relative []float64, on []bool) {
	ref := -120.0
	for _, v := range full {
		ref = math.Max(ref, v)
	}
	relative, on = make([]float64, len(full)), make([]bool, len(full))
	for b := range full {
		relative[b] = -120
		if ref > -120 && full[b] > -120 {
			relative[b] = round(math.Max(-120, full[b]-ref), 1)
		}
		on[b] = test[b] >= silentDB && test[b]-ref >= soundingBelowDB
	}
	return relative, on
}

// findSilences scans the mix in 10 ms blocks.
func findSilences(mix stereo, rate int, clock *project.TempoMap, base int64) []Silence {
	block := max(1, rate/100)
	out := []Silence{}
	start := -1
	flush := func(end int) {
		if start >= 0 && float64(end-start) >= silenceMinS*float64(rate) {
			out = append(out, Silence{Start: positionAt(clock, base, start, rate), End: positionAt(clock, base, end, rate), LengthS: round(float64(end-start)/float64(rate), 3)})
		}
		start = -1
	}
	for pos := 0; pos < mix.Len(); pos += block {
		end := min(mix.Len(), pos+block)
		if rmsDB(mix.Slice(pos, end)) < silenceBelowDBFS {
			if start < 0 {
				start = pos
			}
			continue
		}
		flush(pos)
	}
	flush(mix.Len())
	return out
}

// noteSig is one note start as the repeat counts compare it: its offset in
// the bar, its notes, its velocity (after humanize) and its length when the
// renderer reported a release (-1 otherwise).
type noteSig struct {
	offset   int64
	length   int64
	velocity uint8
	count    uint8
	notes    [4]uint8
}

// repeats is a track's bar-to-bar repetition, from the render's note events.
type repeats struct {
	noteBars, previous, fourBefore, longestRun int
}

// barRepeats counts, per track, the bars whose played notes equal the bar
// before and the bar four before. Only bars of the analyzed range count, and
// a bar with no note start never counts as a repeat.
func (l *noteLog) barRepeats(tracks int, fromTick int64, bars int) []repeats {
	sigs := make([][][]noteSig, tracks)
	for ti := range sigs {
		sigs[ti] = make([][]noteSig, bars)
	}
	for _, n := range l.notes {
		if n.track >= tracks || n.tick < fromTick {
			continue
		}
		b := int((n.tick - fromTick) / seq.TicksPerBar)
		if b >= bars {
			continue
		}
		s := noteSig{offset: (n.tick - fromTick) % seq.TicksPerBar, length: -1, velocity: n.velocity, count: n.count, notes: n.notes}
		if n.off && !n.replaced {
			s.length = n.end - n.tick
		}
		sigs[n.track][b] = append(sigs[n.track][b], s)
	}
	out := make([]repeats, tracks)
	for ti := range sigs {
		for b := range sigs[ti] {
			bar := sigs[ti][b]
			sort.Slice(bar, func(i, j int) bool {
				x, y := bar[i], bar[j]
				if x.offset != y.offset {
					return x.offset < y.offset
				}
				if x.notes != y.notes {
					return slices.Compare(x.notes[:], y.notes[:]) < 0
				}
				if x.velocity != y.velocity {
					return x.velocity < y.velocity
				}
				if x.count != y.count {
					return x.count < y.count
				}
				return x.length < y.length
			})
		}
		r := &out[ti]
		run := 1
		for b, bar := range sigs[ti] {
			if len(bar) == 0 {
				run = 1
				continue
			}
			r.noteBars++
			if b >= 4 && slices.Equal(bar, sigs[ti][b-4]) {
				r.fourBefore++
			}
			if b >= 1 && slices.Equal(bar, sigs[ti][b-1]) {
				r.previous++
				run++
				r.longestRun = max(r.longestRun, run)
			} else {
				run = 1
			}
		}
	}
	return out
}
