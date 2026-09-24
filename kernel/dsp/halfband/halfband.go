// Package halfband implements the 43-tap Kaiser FIR used on both sides
// of Cicada's 2x acid filter section.
package halfband

import "math"

const Taps = 43

const center = (Taps - 1) / 2
const beta = 6.5

type FIR struct {
	coefficients [Taps]float64
	history      [2 * Taps]float64
	position     int
	zeroStuffed  bool
}

// New generates the decision-0002 half-band kernel once, outside the audio loop.
func New() FIR {
	f := FIR{zeroStuffed: true}
	denominator := besselI0(beta)
	var sum float64
	for n := 0; n < Taps; n++ {
		m := n - center
		if m != 0 && m%2 == 0 {
			continue
		}
		sinc := 1.0
		if m != 0 {
			sign := 1.0
			if ((m-1)/2)&1 != 0 {
				sign = -1
			}
			sinc = sign / (math.Pi * float64(m) / 2)
		}
		ratio := float64(m) / center
		window := besselI0(beta*math.Sqrt(1-ratio*ratio)) / denominator
		f.coefficients[n] = sinc * window
		sum += f.coefficients[n]
	}
	for n := range f.coefficients {
		f.coefficients[n] /= sum
	}
	return f
}

func besselI0(x float64) float64 {
	term, sum := 1.0, 1.0
	for k := 1; k <= 24; k++ {
		term *= x * x / (4 * float64(k*k))
		sum += term
	}
	return sum
}

func (f *FIR) Coefficients() [Taps]float64 { return f.coefficients }

func (f *FIR) Reset() {
	f.history = [2 * Taps]float64{}
	f.position = 0
	f.zeroStuffed = true
}

// Push filters one sample at the oversampled rate. It evaluates the center
// tap and the 22 nonzero side taps; no allocation or coefficient work occurs.
func (f *FIR) Push(input float64) float64 {
	f.zeroStuffed = false
	return f.push(input)
}

func (f *FIR) push(input float64) float64 {
	f.history[f.position] = input
	f.history[f.position+Taps] = input
	var output float64
	// The mirrored history removes wrap branches while keeping the same
	// increasing-tap accumulation order and exact sample values.
	for tap := 0; tap < center; tap += 2 {
		index := f.position + Taps - tap
		output += f.coefficients[tap] * f.history[index]
	}
	index := f.position + Taps - center
	output += f.coefficients[center] * f.history[index]
	for tap := center + 1; tap < Taps; tap += 2 {
		index := f.position + Taps - tap
		output += f.coefficients[tap] * f.history[index]
	}
	f.position++
	if f.position == Taps {
		f.position = 0
	}
	return output
}

// Upsample zero-stuffs and scales by two to preserve passband amplitude.
func (f *FIR) Upsample(input float64) (first, second float64) {
	first = 2 * f.push(input)
	if !f.zeroStuffed {
		return first, 2 * f.push(0)
	}
	// With zero stuffing, every side tap of the second phase multiplies an
	// inserted zero. Only the center tap can see an input sample.
	f.history[f.position] = 0
	f.history[f.position+Taps] = 0
	second = 2 * (0.0 + f.coefficients[center]*f.history[f.position+Taps-center])
	f.position++
	if f.position == Taps {
		f.position = 0
	}
	return first, second
}

// Downsample applies the matching FIR and keeps one of the two output phases.
func (f *FIR) Downsample(first, second float64) float64 {
	// The first filtered phase is discarded. Advance its history without
	// evaluating the 23 nonzero taps, then render the retained phase.
	f.history[f.position] = first
	f.history[f.position+Taps] = first
	f.position++
	if f.position == Taps {
		f.position = 0
	}
	return f.Push(second)
}

// Checksum records the exact coefficient bits without a host hash dependency.
func (f *FIR) Checksum() uint64 {
	const prime uint64 = 1099511628211
	hash := uint64(14695981039346656037)
	for _, coefficient := range f.coefficients {
		bits := math.Float64bits(coefficient)
		for i := 0; i < 8; i++ {
			hash ^= uint64(byte(bits >> (8 * i)))
			hash *= prime
		}
	}
	return hash
}
