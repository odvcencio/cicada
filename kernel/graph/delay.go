package graph

import "math"

// DelaySamples is the ring storage reserved per voice, in float32 samples.
func (p Program) DelaySamples() int {
	return delaySamples(&p)
}

func delaySamples(p *Program) int {
	count := 0
	for i := 0; i < int(p.Len); i++ {
		if p.Nodes[i].Op == Delay || p.Nodes[i].Op == Comb {
			count += MaxDelaySamples
		}
	}
	return count
}

// ParameterError identifies a statically invalid primitive argument. Hosts
// can attach CICADA-PARAM and the authored argument's source position.
type ParameterError struct {
	Node, Input int
	Message     string
}

func (e *ParameterError) Error() string { return e.Message }

func validateDelayControls(p *Program, rate int) error {
	return validateDelayPitch(p, rate, 0)
}

// ValidateDelayPitch checks pitch-derived controls for a score note. Zero
// leaves pitch unknown, so instrument defaults can be checked independently.
func ValidateDelayPitch(p Program, rate int, pitch float32) error {
	return validateDelayPitch(&p, rate, pitch)
}

func validateDelayPitch(p *Program, rate int, pitch float32) error {
	var values [MaxNodes]float32
	var known [MaxNodes]bool
	staticValuesInto(p, float32(rate), pitch, &values, &known)
	for i := 0; i < int(p.Len); i++ {
		n := p.Nodes[i]
		if n.Op != Delay && n.Op != Comb {
			continue
		}
		if known[n.B] {
			ms := values[n.B]
			minSamples := float32(1)
			if n.Op == Comb {
				minSamples = 4
			}
			if !finite(ms) || ms*float32(rate)/1000 < minSamples || ms*float32(rate)/1000 > MaxDelaySamples {
				return &ParameterError{i, 1, "delay time is out of range: delay needs 1..4096 samples, comb needs 4..4096 samples at the render rate"}
			}
		}
		if n.Op == Comb {
			for j, index := range []uint8{n.C, uint8(n.Value)} {
				if known[index] && (!finite(values[index]) || values[index] < 0 || values[index] >= 1) {
					return &ParameterError{i, j + 2, "comb feedback and damping must be unit values from 0 inclusive to 1 exclusive"}
				}
			}
		}
	}
	return nil
}

// StaticValues evaluates control arithmetic only. A positive pitch makes
// pitch-derived times known, for score-note validation outside rendering.
func (p Program) StaticValues(rate, pitch float32) ([MaxNodes]float32, [MaxNodes]bool) {
	var values [MaxNodes]float32
	var known [MaxNodes]bool
	staticValuesInto(&p, rate, pitch, &values, &known)
	return values, known
}

// Fill prepared validation buffers without returning fixed arrays by value;
// TinyGo would otherwise flatten them into graph-construction code.
func staticValuesInto(p *Program, rate, pitch float32, values *[MaxNodes]float32, known *[MaxNodes]bool) {
	clear(values[:])
	clear(known[:])
	for i := 0; i < int(p.Len); i++ {
		n := p.Nodes[i]
		a, b := values[n.A], values[n.B]
		switch n.Op {
		case Constant:
			values[i], known[i] = n.Value, true
		case SampleRate:
			values[i], known[i] = rate, true
		case Pitch:
			values[i], known[i] = pitch, pitch > 0
		case Add, Subtract, Multiply, Divide, Period:
			known[i] = known[n.A] && known[n.B]
			switch n.Op {
			case Add:
				values[i] = a + b
			case Subtract:
				values[i] = a - b
			case Multiply:
				values[i] = a * b
			case Divide, Period:
				values[i] = float32(math.NaN())
				if b != 0 {
					values[i] = a / b
				}
				if n.Op == Period {
					values[i] *= 1000
				}
			}
		case Clamp:
			known[i] = known[n.A] && known[n.B] && known[n.C]
			values[i] = clamp(a, b, values[n.C])
		case Exp2:
			known[i] = known[n.A]
			values[i] = float32(math.Exp2(float64(clamp(a, -8, 8))))
		}
	}
}

func finite(x float32) bool { return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0) }
func finiteClamp(x, lo, hi float32) float32 {
	if !finite(x) {
		return lo
	}
	return clamp(x, lo, hi)
}

type delayState struct {
	write                     int
	previous, allpass, damped float32
	period, damping           float32
	integer                   int
	coefficient               float32
}

// Linear interpolation is bounded and has no recursive state, making delay
// suitable for modulated taps. Reading before writing supports 4096 samples.
func (s *delayState) linear(ring []float32, input, samples float32) float32 {
	whole := int(samples)
	fraction := samples - float32(whole)
	// Read before overwriting: a full-ring delay must see the oldest sample.
	a := ring[(s.write-whole)&(MaxDelaySamples-1)]
	b := ring[(s.write-whole-1)&(MaxDelaySamples-1)]
	y := a + fraction*(b-a)
	ring[s.write] = input
	s.write = (s.write + 1) & (MaxDelaySamples - 1)
	return y
}

// The comb uses a first-order allpass so fractional interpolation introduces
// no frequency-dependent loop loss. Time denotes the complete loop period.
// The one-pole H(z)=(1-d)/(1-d*z^-1) contributes phase delay; remove its exact
// delay at the requested fundamental, then tune the allpass at that frequency.
// Cache the trigonometry until period or damping changes. The fractional
// section spans [0.5,1.5) samples, keeping its pole away from the unit circle
// even at the minimum four-sample period.
func (s *delayState) comb(ring []float32, input, period, feedback, damping float32) float32 {
	if s.period != period || s.damping != damping {
		w := 2 * math.Pi / float64(period)
		phaseDelay := math.Atan2(float64(damping)*math.Sin(w), 1-float64(damping)*math.Cos(w)) / w
		delay := math.Max(2, float64(period)-phaseDelay)
		s.integer = int(delay - .5)
		fraction := delay - float64(s.integer)
		ratio := math.Tan(w*fraction/2) / math.Tan(w/2)
		s.coefficient = float32((1 - ratio) / (1 + ratio))
		s.period, s.damping = period, damping
	}
	x := ring[(s.write-s.integer)&(MaxDelaySamples-1)]
	y := s.coefficient*x + s.previous - s.coefficient*s.allpass
	s.previous, s.allpass = x, y
	s.damped = (1-damping)*y + damping*s.damped
	ring[s.write] = input + feedback*s.damped
	s.write = (s.write + 1) & (MaxDelaySamples - 1)
	return y
}
