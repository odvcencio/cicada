package graph

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
)

// TestCoefficientCacheMatchesUncachedPrograms compares every node and output
// against the interpreter before coefficient caching, including cache misses,
// releases, slides, resets, clamped inputs and non-finite intermediate values.
func TestCoefficientCacheMatchesUncachedPrograms(t *testing.T) {
	seed := uint32(4242)
	random := func() uint32 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return seed
	}
	for _, rate := range []int{44100, 48000, 96000} {
		for trial := 0; trial < 128; trial++ {
			p := Program{Len: MaxNodes, Output: MaxNodes - 1, GlideMS: 60}
			p.Nodes[0] = Node{Op: Pitch}
			p.Nodes[1] = Node{Op: Gate}
			p.Nodes[2] = Node{Op: Velocity}
			p.Nodes[3] = Node{Op: SampleRate}
			for i := 4; i < MaxNodes; i++ {
				p.Nodes[i] = Node{Op: Op(1 + random()%uint32(Clamp)), A: uint8(random() % uint32(i)), B: uint8(random() % uint32(i)), C: uint8(random() % uint32(i)), Value: float32(int32(random()%60001) - 30000)}
			}
			cached, err := NewVoice(p, rate)
			if err != nil {
				t.Fatal(err)
			}
			reference := *cached
			for sample := 0; sample < 8192; sample++ {
				// Alternating held controls and per-sample changes exercises both paths.
				if sample%256 < 128 || sample%256 == 128 {
					note, velocity := uint8(random()%128), uint8(random()%128)
					slide := sample%3 != 0
					cached.NoteOn(note, velocity, slide)
					reference.NoteOn(note, velocity, slide)
				}
				if sample%193 == 0 {
					cached.NoteOff()
					reference.NoteOff()
				}
				if sample%997 == 0 {
					cached.Reset()
					reference.Reset()
				}
				got, want := cached.Next(), reference.nextUncached()
				if math.Float32bits(got) != math.Float32bits(want) {
					t.Fatalf("rate=%d program=%d sample=%d: output bits %08x != %08x", rate, trial, sample, math.Float32bits(got), math.Float32bits(want))
				}
				for i := 0; i < int(p.Len); i++ {
					if math.Float32bits(cached.values[i]) != math.Float32bits(reference.values[i]) {
						t.Fatalf("rate=%d program=%d sample=%d node=%d op=%d: value bits differ", rate, trial, sample, i, p.Nodes[i].Op)
					}
				}
			}
		}
	}
	t.Log("METRIC BITEXACT path=graph rates_hz=44100,48000,96000 programs=384 samples=3145728 node_comparisons=402653184 mismatches=0")
}

// nextUncached retains the original per-sample formulas as the regression oracle.
func (v *Voice) nextUncached() float32 {
	if v.gliding {
		v.pitchLog += (v.targetLog - v.pitchLog) * v.pitchAlpha
		v.pitch = float32(fastmath.Exp2(v.pitchLog))
	}
	for i := 0; i < int(v.program.Len); i++ {
		n := v.program.Nodes[i]
		s := &v.states[i]
		a, b, c := v.values[n.A], v.values[n.B], v.values[n.C]
		var y float32
		switch n.Op {
		case Pitch:
			y = v.pitch
		case Gate:
			y = v.gate
		case Velocity:
			y = v.velocity
		case SampleRate:
			y = v.sampleRate
		case Constant:
			y = n.Value
		case Add:
			y = a + b
		case Subtract:
			y = a - b
		case Multiply:
			y = a * b
		case Divide:
			if b != 0 {
				y = a / b
			}
		case Saw, Square, Sine:
			frequency := clamp(a, 0, v.sampleRate*0.49)
			dt := frequency / v.sampleRate
			phase := s.phase
			if dt > 0 {
				switch n.Op {
				case Saw:
					y = 2*phase - 1 - polyBLEP(phase, dt)
				case Square:
					if phase < 0.5 {
						y = 1
					} else {
						y = -1
					}
					y += polyBLEP(phase, dt) - polyBLEP(frac(phase+0.5), dt)
				case Sine:
					y = float32(math.Sin(2 * math.Pi * float64(phase)))
				}
				s.phase = frac(phase + dt)
			}
		case Noise:
			x := s.noise
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			s.noise = x
			y = float32(int32(x)) / 2147483648
		case Envelope:
			// b is time in milliseconds. Gate release is 30 ms at most.
			ms := clamp(b, 1, 30_000)
			if a <= 0 && ms > 30 {
				ms = 30
			}
			y = s.env
			s.env *= float32(math.Exp(-4600 / float64(ms*v.sampleRate)))
		case Ladder, Diode:
			frequency := clamp(b, 20, v.sampleRate*0.45)
			g := float32(math.Tan(math.Pi * float64(frequency/v.sampleRate)))
			coef := g / (1 + g)
			feedback := float32(3.2) * clamp(c, 0, 1)
			if n.Op == Diode {
				feedback = 2.8 * clamp(c, 0, 1)
			}
			input := float32(math.Tanh(float64(a - feedback*s.filter[3])))
			for stage := 0; stage < 4; stage++ {
				s.filter[stage] += coef * (input - s.filter[stage])
				input = s.filter[stage]
			}
			y = s.filter[3]
		case Lowpass, Highpass:
			frequency := clamp(b, 20, v.sampleRate*0.45)
			coef := float32(1 - math.Exp(-2*math.Pi*float64(frequency/v.sampleRate)))
			s.filter[0] += coef * (a - s.filter[0])
			y = s.filter[0]
			if n.Op == Highpass {
				y = a - y
			}
		case Mix:
			blend := clamp(c, 0, 1)
			y = a*(1-blend) + b*blend
		case Tanh:
			y = float32(math.Tanh(float64(a)))
		case Exp2:
			y = float32(math.Exp2(float64(clamp(a, -8, 8))))
		case Clamp:
			y = clamp(a, b, c)
		}
		v.values[i] = y
	}
	out := v.values[v.program.Output]
	if math.IsNaN(float64(out)) || math.IsInf(float64(out), 0) {
		v.Reset()
		return 0
	}
	return out
}
