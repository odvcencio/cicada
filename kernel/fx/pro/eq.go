package pro

import "math"

type EQType uint8

const (
	Peak EQType = iota
	LowShelf
	HighShelf
	Highpass
	Lowpass
)

// Band uses Q for peak/pass filters; shelves use a unity shelf slope. Design
// equations follow https://www.w3.org/TR/audio-eq-cookbook/.
type Band struct {
	Enabled     bool
	Type        EQType
	FrequencyHz float64
	Q           float64
	GainDB      float64
}

type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             [2]float64
}

func (f *biquad) process(input float64, channel int) float64 {
	output := f.b0*input + f.z1[channel]
	f.z1[channel] = clean(f.b1*input - f.a1*output + f.z2[channel])
	f.z2[channel] = clean(f.b2*input - f.a2*output)
	return output
}

type EQ struct {
	filters [4]biquad
	active  [4]bool
}

func NewEQ(sampleRate int, bands [4]Band) (*EQ, error) {
	if !validRate(sampleRate) {
		return nil, ErrSampleRate
	}
	e := &EQ{}
	for i, band := range bands {
		if !band.Enabled {
			continue
		}
		if band.Type > Lowpass || !finite(band.FrequencyHz) || !finite(band.Q) || !finite(band.GainDB) ||
			band.FrequencyHz < 10 || band.FrequencyHz > .45*float64(sampleRate) || band.Q < .1 || band.Q > 20 ||
			band.GainDB < -24 || band.GainDB > 24 {
			return nil, ErrParams
		}
		// A zero-gain peak or shelf is an exact bypass, avoiding needless roundoff.
		if band.GainDB == 0 && band.Type <= HighShelf {
			continue
		}
		e.filters[i] = designBand(sampleRate, band)
		e.active[i] = true
	}
	return e, nil
}

func designBand(rate int, p Band) biquad {
	w := 2 * math.Pi * p.FrequencyHz / float64(rate)
	s, c := math.Sincos(w)
	a := dbGain(p.GainDB / 2)
	alpha := s / (2 * p.Q)
	var b0, b1, b2, a0, a1, a2 float64
	switch p.Type {
	case Peak:
		b0, b1, b2 = 1+alpha*a, -2*c, 1-alpha*a
		a0, a1, a2 = 1+alpha/a, -2*c, 1-alpha/a
	case LowShelf, HighShelf:
		// Fixed slope S=1 keeps shelves monotonic at all gain settings.
		beta := math.Sqrt(2*a) * s
		if p.Type == LowShelf {
			b0 = a * ((a + 1) - (a-1)*c + beta)
			b1 = 2 * a * ((a - 1) - (a+1)*c)
			b2 = a * ((a + 1) - (a-1)*c - beta)
			a0 = (a + 1) + (a-1)*c + beta
			a1 = -2 * ((a - 1) + (a+1)*c)
			a2 = (a + 1) + (a-1)*c - beta
		} else {
			b0 = a * ((a + 1) + (a-1)*c + beta)
			b1 = -2 * a * ((a - 1) + (a+1)*c)
			b2 = a * ((a + 1) + (a-1)*c - beta)
			a0 = (a + 1) - (a-1)*c + beta
			a1 = 2 * ((a - 1) - (a+1)*c)
			a2 = (a + 1) - (a-1)*c - beta
		}
	case Highpass:
		b0, b1, b2 = (1+c)/2, -(1 + c), (1+c)/2
		a0, a1, a2 = 1+alpha, -2*c, 1-alpha
	case Lowpass:
		b0, b1, b2 = (1-c)/2, 1-c, (1-c)/2
		a0, a1, a2 = 1+alpha, -2*c, 1-alpha
	}
	return biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0}
}

func (e *EQ) Process(left, right float32) (float32, float32) {
	l, r := float64(left), float64(right)
	for i := range e.filters {
		if e.active[i] {
			l = e.filters[i].process(l, 0)
			r = e.filters[i].process(r, 1)
		}
	}
	return float32(l), float32(r)
}

func (e *EQ) Reset() {
	for i := range e.filters {
		e.filters[i].z1, e.filters[i].z2 = [2]float64{}, [2]float64{}
	}
}

func (e *EQ) LatencyFrames() int { return 0 }
