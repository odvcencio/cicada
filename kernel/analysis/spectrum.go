package analysis

import (
	"fmt"
	"math"
)

const (
	// Bands is the number of log-spaced spectrum bands.
	Bands = 64
	// LowHz is the lower edge of the first band.
	LowHz = 20.0
	// FloorDB is the lowest level a band reports.
	FloorDB = -120.0

	minSampleRate = 8000
	maxHighHz     = 20000.0
	nyquistShare  = 0.45
)

// windowSize returns the FFT length, about 85 ms of audio at any rate.
func windowSize(sampleRate int) int {
	switch {
	case sampleRate <= 48000:
		return 4096
	case sampleRate <= 96000:
		return 8192
	default:
		return 16384
	}
}

func highHz(sampleRate int) float64 {
	return math.Min(maxHighHz, nyquistShare*float64(sampleRate))
}

// BandHz returns the lower and upper edge of band i in Hz. Bands are spaced
// evenly on a log axis from 20 Hz to min(20 kHz, 0.45 x sampleRate). It returns
// zeros for an index outside [0, Bands) or a sample rate below 8 kHz.
func BandHz(sampleRate, i int) (low, high float64) {
	if i < 0 || i >= Bands || sampleRate < minSampleRate {
		return 0, 0
	}
	ratio := highHz(sampleRate) / LowHz
	return LowHz * math.Pow(ratio, float64(i)/Bands), LowHz * math.Pow(ratio, float64(i+1)/Bands)
}

// bandSpan lists the FFT bins a band draws power from. Bin k stands for the
// frequencies within half a bin of its centre, and a band takes the share of
// each bin that lies between its edges. So the bands split the spectrum's
// power between them, with no gap and no double counting, whether a band is
// wider or narrower than a bin.
type bandSpan struct {
	first, last           int     // inclusive range of bins that overlap the band
	firstShare, lastShare float64 // share of the first and last bin inside the band
}

// Spectrum computes a Hann-windowed, 64-band power spectrum of the mono mix
// (L+R)/2 over a sliding window. It is not safe for concurrent use.
type Spectrum struct {
	plan   *fftPlan
	ring   []float32
	head   int // next write index; also the oldest sample once full
	filled int
	re, im []float64
	power  []float64
	spans  [Bands]bandSpan
	scale  float64
}

// NewSpectrum builds an analyzer for the given sample rate (8 kHz or higher).
func NewSpectrum(sampleRate int) (*Spectrum, error) {
	if sampleRate < minSampleRate {
		return nil, fmt.Errorf("analysis: sample rate %d Hz is below %d Hz", sampleRate, minSampleRate)
	}
	n := windowSize(sampleRate)
	s := &Spectrum{
		plan: newFFTPlan(n), ring: make([]float32, n),
		re: make([]float64, n), im: make([]float64, n), power: make([]float64, n/2+1),
	}
	// A sine of amplitude A puts N*A^2/4*sum(w^2) of power in the positive
	// half of the spectrum (Parseval). This scale makes the band sum equal A^2.
	s.scale = 4 / (float64(n) * s.plan.winSumSq)
	binHz := float64(sampleRate) / float64(n)
	for i := range s.spans {
		low, high := BandHz(sampleRate, i)
		low, high = low/binHz, high/binHz // band edges in bins
		first := int(math.Floor(low + 0.5))
		last := min(int(math.Floor(high+0.5)), n/2)
		span := bandSpan{first: first, last: last, firstShare: high - low}
		if last > first {
			span.firstShare = float64(first) + 0.5 - low
			span.lastShare = min(high-(float64(last)-0.5), 1)
		}
		s.spans[i] = span
	}
	return s, nil
}

// Reset clears the window.
func (s *Spectrum) Reset() {
	clear(s.ring)
	s.head, s.filled = 0, 0
}

// Ready reports whether a full window of audio has arrived since the last
// reset.
func (s *Spectrum) Ready() bool { return s.filled >= len(s.ring) }

// Process appends the mono mix of the two channels. If the slices differ in
// length, the shorter one sets the frame count. Non-finite samples count as 0.
func (s *Spectrum) Process(left, right []float32) {
	n := min(len(left), len(right))
	size := len(s.ring)
	for i := 0; i < n; i++ {
		l, r := left[i], right[i]
		if l != l || r != r || l-l != 0 || r-r != 0 { // NaN or Inf
			l, r = 0, 0
		}
		s.ring[s.head] = l*0.5 + r*0.5 // halve first: l+r can overflow float32
		s.head++
		if s.head == size {
			s.head = 0
		}
	}
	s.filled = min(s.filled+n, size)
}

// Bands writes the level of each band in dBFS: the power of the mono mix
// between the band's edges, on a scale where a full-scale sine carries 0 dB.
// The bands add up to the signal's power between 20 Hz and the top edge, and
// steady noise reads evenly across wide and narrow bands. Levels floor at
// -120 dB.
//
// The window resolves about 12 Hz, and a tone occupies four bins. A sine in
// the middle of a band reads its level from about 350 Hz up; a sine near a
// band edge splits across two bands. Lower bands are narrower than a tone's
// four bins, so a low tone spreads over its neighbours, which still add up to
// its level: a full-scale 50 Hz sine reads about -5.5 dB in its own band.
func (s *Spectrum) Bands(out *[Bands]float32) {
	n := len(s.ring)
	w := s.plan.window
	idx := s.head
	for k := 0; k < n; k++ {
		s.re[k] = float64(s.ring[idx]) * w[k]
		s.im[k] = 0
		idx++
		if idx == n {
			idx = 0
		}
	}
	s.plan.transform(s.re, s.im)
	for k := 0; k <= n/2; k++ {
		s.power[k] = (s.re[k]*s.re[k] + s.im[k]*s.im[k]) * s.scale
	}
	for i, span := range s.spans {
		p := s.power[span.first] * span.firstShare
		if span.last > span.first {
			for k := span.first + 1; k < span.last; k++ {
				p += s.power[k]
			}
			p += s.power[span.last] * span.lastShare
		}
		out[i] = powerToDB(p)
	}
}

func powerToDB(p float64) float32 {
	if !(p > 1e-12) || math.IsInf(p, 1) { // also catches NaN; never returns a non-finite level
		return FloorDB
	}
	return float32(10 * math.Log10(p))
}
