package scoreaudition

import (
	"math"
	"sort"
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

// BandCollisions lists the bars where two tracks each hold more than 25% of
// one band, and how often each pair of tracks is the colliding pair (the two
// largest shares).
type BandCollisions struct {
	Band  string      `json:"band"`
	Bars  []int       `json:"bars"`
	Pairs []PairCount `json:"pairs"`
}

// PairCount is a pair of tracks and the bars in which they collide.
type PairCount struct {
	Tracks [2]string `json:"tracks"`
	Bars   int       `json:"bars"`
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
func bandLevelDB(sum float64, frames, n int) float64 {
	if frames == 0 || sum <= 0 {
		return -120
	}
	return math.Max(-120, 10*math.Log10(sum/float64(frames)/(3*float64(n)*float64(n)/16)))
}

// owner reads one band's per-track energies, all summed over the same
// frames. loudest is the band's largest per-frame mean in any bar, span the
// largest per-frame mean of any band over these frames, and top the largest
// per-frame mean of any band in any bar.
func owner(band int, energy []float64, ids []string, use []bool, frames, n int, loudest, span, top float64) BandOwner {
	o := BandOwner{Band: bandNames[band]}
	var total float64
	for ti, e := range energy {
		if use[ti] {
			total += e
		}
	}
	o.LevelDB = round(bandLevelDB(total, frames, n), 1)
	mean := total / float64(max(frames, 1))
	if total <= 0 || loudest <= 0 || span <= 0 || top <= 0 || frames == 0 || 10*math.Log10(mean/loudest) < -quietBandDB ||
		10*math.Log10(mean/span) < -quietSpanDB || 10*math.Log10(mean/top) < -quietRangeDB {
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
