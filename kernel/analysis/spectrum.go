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

type bandSpan struct {
	first, last int     // inclusive bin range whose centres lie in the band
	pos         float64 // fractional bin of the band centre, used when no bin falls inside
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
		first := int(math.Ceil(low / binHz))
		last := int(math.Ceil(high/binHz)) - 1
		if i == Bands-1 {
			last = int(math.Floor(high / binHz))
		}
		if last > n/2 {
			last = n / 2
		}
		s.spans[i] = bandSpan{first: first, last: last, pos: math.Sqrt(low*high) / binHz}
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
		s.ring[s.head] = (l + r) * 0.5
		s.head++
		if s.head == size {
			s.head = 0
		}
	}
	s.filled = min(s.filled+n, size)
}

// Bands writes the level of each band in dBFS. A full-scale sine reads 0 dB in
// the band that holds it (a sine near a band edge splits across two bands).
// Levels floor at -120 dB. Bands narrower than one FFT bin read the
// interpolated bin power at the band centre.
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
		var p float64
		if span.last >= span.first {
			for k := span.first; k <= span.last; k++ {
				p += s.power[k]
			}
		} else {
			k0 := int(span.pos)
			f := span.pos - float64(k0)
			k1 := min(k0+1, n/2)
			p = (1-f)*s.power[k0] + f*s.power[k1]
		}
		out[i] = powerToDB(p)
	}
}

func powerToDB(p float64) float32 {
	if !(p > 1e-12) { // also catches NaN
		return FloorDB
	}
	return float32(10 * math.Log10(p))
}
