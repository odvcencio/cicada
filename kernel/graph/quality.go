package graph

import (
	"m31labs.dev/cicada/kernel/dsp/halfband"
	"m31labs.dev/cicada/kernel/voice/acid"
	"math"
)

// Only graphs opting into the new primitives use this path. Legacy graph
// programs keep their original sample rate, phases, and filter behavior.
type qualityState struct {
	nodes            []qualityNode
	down             halfband.FIR
	baseRate         float32
	last, previous   float32
	fade, fadeFrames int
}
type qualityNode struct {
	phase      float64
	oscillator *acid.TableOscillator
	envelope   adsrState
	filter     svfState
}

func usesQuality(p Program) bool {
	for _, n := range p.Nodes[:p.Len] {
		if n.Op == ADSR || n.Op == Pulse || n.Op == SVF {
			return true
		}
	}
	return false
}

//go:noinline
func newQualityState(p Program, rate int) *qualityState {
	q := new(qualityState)
	// Allocate only the validated node count during voice construction. A
	// giant embedded zero array expands into individual stores in TinyGo.
	q.nodes = make([]qualityNode, int(p.Len))
	q.down = halfband.New()
	q.baseRate, q.fadeFrames = float32(rate), rate/500
	for i, n := range p.Nodes[:p.Len] {
		if n.Op == Saw || n.Op == Square || n.Op == Pulse {
			q.nodes[i].oscillator, _ = acid.NewTableOscillator(rate)
		}
	}
	return q
}
func (q *qualityState) reset() {
	q.down.Reset()
	q.last, q.previous, q.fade = 0, 0, 0
	for i := range q.nodes {
		q.nodes[i].phase = 0
		q.nodes[i].envelope = adsrState{}
		q.nodes[i].filter = svfState{}
	}
}
func (q *qualityState) retrigger() {
	q.previous, q.fade = q.last, q.fadeFrames
	for i := range q.nodes {
		q.nodes[i].phase = 0
		q.nodes[i].envelope = adsrState{}
	}
}
func (q *qualityState) oscillate(i int, op Op, hz, width, internalRate float32) float32 {
	s := &q.nodes[i]
	frequency := float64(hz)
	// Out-of-band additive partials disappear instead of turning into an
	// unrelated tone at the frequency clamp (or folding into the audible band).
	if frequency <= 0 || frequency >= float64(q.baseRate)*0.49 || math.IsNaN(frequency) {
		return 0
	}
	var out float64
	switch op {
	case Saw:
		out = s.oscillator.Saw(s.phase, frequency)
	case Square, Pulse:
		w := float64(clamp(width, 0.02, 0.98))
		// Center variable-duty pulses before filtering and amplification.
		// Acid keeps its historical bipolar pulse behavior in its own voice.
		out = s.oscillator.Pulse(s.phase, frequency, w) - (2*w - 1)
	case Sine:
		out = math.Sin(2 * math.Pi * s.phase)
	}
	s.phase += frequency / float64(internalRate)
	if s.phase >= 1 {
		s.phase--
	}
	return float32(out)
}

type adsrState struct {
	level, start, target, progress, coefficient float64
	remaining                                   int
	stage                                       uint8
	held                                        bool
}

const (
	envOff uint8 = iota
	envAttack
	envDecay
	envSustain
	envRelease
)
const envelopeFloor = 0.006737946999085467 // exp(-5)
func (s *adsrState) segment(stage uint8, target float64, ms, rate float32) {
	s.stage, s.start, s.target, s.progress = stage, s.level, target, 1
	s.remaining = int(float64(clamp(ms, 0, 30_000))*float64(rate)/1000 + 0.5)
	if s.remaining > 0 {
		s.coefficient = math.Exp(-5 / float64(s.remaining))
	}
}
func (s *adsrState) next(held bool, attack, decay, sustain, release, rate float32) float32 {
	if held != s.held {
		if held {
			s.segment(envAttack, 1, attack, rate)
		} else {
			s.segment(envRelease, 0, release, rate)
		}
		s.held = held
	}
	for range 3 {
		switch s.stage {
		case envOff:
			return 0
		case envSustain:
			s.level = float64(clamp(sustain, 0, 1))
			return float32(s.level)
		}
		if s.remaining > 0 {
			s.progress *= s.coefficient
			s.level = s.target + (s.start-s.target)*(s.progress-envelopeFloor)/(1-envelopeFloor)
			s.remaining--
			if s.remaining > 0 {
				return float32(s.level)
			}
			s.level = s.target
			switch s.stage {
			case envAttack:
				s.segment(envDecay, float64(clamp(sustain, 0, 1)), decay, rate)
			case envDecay:
				s.stage = envSustain
			case envRelease:
				s.stage = envOff
				s.level = 0
			}
			return float32(s.level)
		}
		s.level = s.target
		switch s.stage {
		case envAttack:
			s.segment(envDecay, float64(clamp(sustain, 0, 1)), decay, rate)
		case envDecay:
			s.stage = envSustain
			return float32(s.level)
		case envRelease:
			s.stage = envOff
			s.level = 0
			return 0
		}
	}
	return float32(s.level)
}

// Trapezoidal-integrator state-variable lowpass, after Andrew Simper's
// optimized linear SVF derivation. Coefficients update only when controls move.
type svfState struct {
	ic1, ic2, a1, a2, a3, cutoff, resonance float64
	ready                                   bool
}

func (s *svfState) next(input, cutoff, resonance, rate, baseRate float32) float32 {
	f := float64(clamp(cutoff, 20, baseRate*0.45))
	r := float64(clamp(resonance, 0, 1))
	if !s.ready || f != s.cutoff || r != s.resonance {
		g := math.Tan(math.Pi * f / float64(rate))
		k := 2 - 1.9*r // finite damping even at maximum resonance
		s.a1 = 1 / (1 + g*(g+k))
		s.a2 = g * s.a1
		s.a3 = g * s.a2
		s.cutoff, s.resonance, s.ready = f, r, true
	}
	v3 := float64(input) - s.ic2
	v1 := s.a1*s.ic1 + s.a2*v3
	v2 := s.ic2 + s.a2*s.ic1 + s.a3*v3
	s.ic1 = 2*v1 - s.ic1
	s.ic2 = 2*v2 - s.ic2
	if math.Abs(s.ic1) < 1e-20 {
		s.ic1 = 0
	}
	if math.Abs(s.ic2) < 1e-20 {
		s.ic2 = 0
	}
	return float32(v2)
}

func (v *Voice) envelopeLevel() (level float32, exists bool) {
	for i, n := range v.program.Nodes[:v.program.Len] {
		var value float32
		switch n.Op {
		case ADSR:
			value = float32(v.quality.nodes[i].envelope.level)
		case Envelope:
			value = v.states[i].env
		default:
			continue
		}
		exists = true
		if value > level {
			level = value
		}
	}
	return
}

func (v *Voice) spreadPhase(seed uint32) {
	if v.quality == nil {
		return
	}
	for i, n := range v.program.Nodes[:v.program.Len] {
		if n.Op == Saw || n.Op == Square || n.Op == Pulse || n.Op == Sine {
			seed = seed*1664525 + 1013904223
			v.quality.nodes[i].phase = float64(seed) / 4294967296
		}
	}
}
