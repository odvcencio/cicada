package scoreaudition

import (
	"math"
	"sort"

	"m31labs.dev/cicada/kernel/dsp/detmath"
)

// The bands that mixing decisions use. Edges are fixed: a band holds the FFT
// bins whose centre frequency f satisfies low <= f < high.
const numBands = 7

var (
	bandNames   = [numBands]string{"sub", "low", "bass", "low_mid", "mid", "presence", "air"}
	bandEdgesHz = [numBands + 1]float64{20, 40, 80, 160, 400, 1600, 5000, 20000}
)

const (
	// collisionSharePct: a band collides when two tracks each hold more than
	// this share of it.
	collisionSharePct = 25.0
	// quietBandDB: a band more than this far under its own loudest bar has
	// no owner there, so a few stray partials in a drop-out do not make one.
	quietBandDB = 30.0
	// quietSpanDB: nor does a band more than this far under the loudest band
	// of the same bar or section: window leakage and attack clicks.
	quietSpanDB = 30.0
	// quietRangeDB: nor a band more than this far under the loudest band of
	// any bar, such as the next bar's first hit seen by a frame of a
	// drop-out bar (a frame reaches up to 85 ms past its sixteenth).
	quietRangeDB = 40.0
)

// BandsReport describes the band measurements and sums up the collisions.
type BandsReport struct {
	Names             []string  `json:"names"`
	EdgesHz           []float64 `json:"edges_hz"`
	CollisionSharePct float64   `json:"collision_share_pct"`
	// A band has no owner in a bar or section when it is more than
	// QuietBelowBandDB under its own loudest bar, more than QuietBelowSpanDB
	// under the loudest band of that bar or section, or more than
	// QuietBelowRangeDB under the loudest band of any bar.
	QuietBelowBandDB  float64 `json:"quiet_below_band_db"`
	QuietBelowSpanDB  float64 `json:"quiet_below_span_db"`
	QuietBelowRangeDB float64 `json:"quiet_below_range_db"`
	// Collisions has one entry per band, in band order.
	Collisions []BandCollisions `json:"collisions"`
}

// BandCollisions sums up one band's collisions over the range: how often each
// pair of tracks is the colliding pair (the two largest shares, each over
// 25%), and, as detail (DropDetail removes it), the bars where they collide.
type BandCollisions struct {
	Band  string      `json:"band"`
	Bars  *[]int      `json:"bars,omitempty"`
	Pairs []PairCount `json:"pairs"`
}

// PairCount is a pair of tracks and the number of bars in which they collide.
type PairCount struct {
	Tracks   [2]string `json:"tracks"`
	BarCount int       `json:"bar_count"`
}

// BandOwner is the ownership of one band in a bar or a section: the summed
// level of the analyzed tracks in the band (dB re a full-scale RMS, so a
// full-scale sine reads -3), the track with the largest share and the
// runner-up. Owner is empty when the band is quiet.
type BandOwner struct {
	Band           string  `json:"band"`
	LevelDB        float64 `json:"level_db"`
	Quiet          bool    `json:"quiet"`
	Owner          string  `json:"owner,omitempty"`
	SharePct       float64 `json:"share_pct"`
	Second         string  `json:"second,omitempty"`
	SecondSharePct float64 `json:"second_share_pct"`
	Collision      bool    `json:"collision"`
}

// SectionBand is a band over a whole section. CollisionBars counts the bars
// whose first tick is in the section and whose band collides.
type SectionBand struct {
	BandOwner
	CollisionBars int `json:"collision_bars"`
}

// bandHop spaces the band frames a quarter window apart: squared Hann
// windows at that hop sum to a constant, so every instant of a bar weighs the
// same whatever the tempo and wherever a hit falls.
const bandHop = chromaFFT / 4

// bandLayout places the band frames. Each bar is measured from its own
// samples only: its frames see the stem with everything outside the bar set
// to zero, so no frame carries sound from a neighbouring bar into it. Frame f
// belongs to bar bar[f] and is centred on sample centre[f] of the range (kept
// inside its bar, for the section a frame counts in); start[f] is the first
// sample its window reads, relative to the bar start. weight[b] is the number
// of frames bar b is worth, its length over the hop.
type bandLayout struct {
	bar, centre, start []int
	barStart           []int
	weight             []float64
}

func newBandLayout(barSamples []int) bandLayout {
	var l bandLayout
	for b := 0; b+1 < len(barSamples); b++ {
		lo, hi := barSamples[b], barSamples[b+1]
		l.barStart = append(l.barStart, lo)
		l.weight = append(l.weight, math.Max(1, float64(hi-lo)/bandHop))
		// Every frame whose window reaches a sample of the bar.
		for j := -1; j*bandHop-chromaFFT/2 < hi-lo; j++ {
			l.bar = append(l.bar, b)
			l.centre = append(l.centre, lo+min(max(j*bandHop, 0), max(hi-lo-1, 0)))
			l.start = append(l.start, j*bandHop-chromaFFT/2)
		}
	}
	return l
}

// bandPower is the power of one stem in each band, frame by frame in the
// layout's order.
func bandPower(s stereo, plan *fftPlan, binBand []int8, layout bandLayout) [][numBands]float64 {
	out := make([][numBands]float64, len(layout.bar))
	buf := newPowBuffers(plan.n)
	for f := 0; f < len(out); {
		b := layout.bar[f]
		first := f
		for f < len(out) && layout.bar[f] == b {
			f++
		}
		lo := min(layout.barStart[b], s.Len())
		hi := s.Len()
		if b+1 < len(layout.barStart) {
			hi = min(layout.barStart[b+1], s.Len())
		}
		buf.frames(s.Slice(lo, hi), plan, f-first, func(i int) int { return layout.start[first+i] }, func(i int, pow []float64) {
			for k, band := range binBand {
				if band >= 0 {
					out[first+i][band] += pow[k]
				}
			}
		})
	}
	return out
}

// binBands maps each bin of an n-point FFT to its band, or -1.
func binBands(n, rate int) []int8 {
	out := make([]int8, n/2+1)
	for k := range out {
		f := float64(k) * float64(rate) / float64(n)
		out[k] = -1
		for b := 0; b < numBands; b++ {
			if f >= bandEdgesHz[b] && f < bandEdgesHz[b+1] {
				out[k] = int8(b)
			}
		}
	}
	return out
}

// bandLevelDB turns a sum of frames' band power (|X|^2 summed over the bins of
// a Hann-windowed n-point frame) into dB re a full-scale RMS: a frame's power
// sum is 3n^2/16 times the mean square of the windowed signal.
func bandLevelDB(sum, frames float64, n int) float64 {
	if frames <= 0 || sum <= 0 {
		return -120
	}
	return math.Max(-120, 10*detmath.Log10(sum/frames/(3*float64(n)*float64(n)/16)))
}

// owner reads one band's per-track energies, all summed over the same
// frames. loudest is the band's largest per-frame mean in any bar, span the
// largest per-frame mean of any band over these frames, and top the largest
// per-frame mean of any band in any bar.
func owner(band int, energy []float64, ids []string, use []bool, frames float64, n int, loudest, span, top float64) BandOwner {
	o := BandOwner{Band: bandNames[band]}
	var total float64
	for ti, e := range energy {
		if use[ti] {
			total += e
		}
	}
	o.LevelDB = round(bandLevelDB(total, frames, n), 1)
	mean := total / math.Max(frames, 1)
	if total <= 0 || loudest <= 0 || span <= 0 || top <= 0 || frames <= 0 || 10*detmath.Log10(mean/loudest) < -quietBandDB ||
		10*detmath.Log10(mean/span) < -quietSpanDB || 10*detmath.Log10(mean/top) < -quietRangeDB {
		o.Quiet = true
		return o
	}
	order := make([]int, 0, len(energy))
	for ti := range energy {
		if use[ti] {
			order = append(order, ti)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return energy[order[i]] > energy[order[j]] })
	o.Owner, o.SharePct = ids[order[0]], round(100*energy[order[0]]/total, 1)
	if len(order) > 1 && energy[order[1]] > 0 {
		o.Second, o.SecondSharePct = ids[order[1]], round(100*energy[order[1]]/total, 1)
		o.Collision = 100*energy[order[0]]/total > collisionSharePct && 100*energy[order[1]]/total > collisionSharePct
	}
	return o
}
