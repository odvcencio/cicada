// Package graph is a fixed-size, pure-Go DSP graph executor. Programs are
// compiled and validated outside the audio callback.
package graph

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
)

const MaxNodes = 128

type Op uint8

const (
	Pitch Op = iota + 1
	Gate
	Velocity
	SampleRate
	Constant
	Add
	Subtract
	Multiply
	Divide
	Saw
	Square
	Sine
	Noise
	Envelope
	Ladder
	Diode
	Lowpass
	Highpass
	Mix
	Tanh
	Exp2
	Clamp
	ADSR
	Pulse
	SVF
)

type Node struct {
	Op    Op
	A     uint8
	B     uint8
	C     uint8
	D     uint8 `json:",omitempty"`
	E     uint8 `json:",omitempty"`
	Value float32
}

type Program struct {
	Nodes   [MaxNodes]Node
	Len     uint8
	Output  uint8
	GlideMS float64 // portamento time constant in milliseconds; zero keeps immediate pitch changes
}

type Error string

func (e Error) Error() string { return string(e) }

type nodeState struct {
	phase  float32
	env    float32
	filter [4]float32
	noise  uint32
}

// Voice owns all state and storage for one instance of a program. Next does
// not allocate, lock, or use goroutines.
type Voice struct {
	program    Program
	values     [MaxNodes]float32
	states     [MaxNodes]nodeState
	sampleRate float32
	pitch      float32
	pitchLog   float64
	targetLog  float64
	pitchAlpha float64
	gliding    bool
	gate       float32
	velocity   float32
	quality    *qualityState
}

//go:noinline
func NewVoice(program Program, sampleRate int) (*Voice, error) {
	if program.Len == 0 || int(program.Len) > MaxNodes || program.Output >= program.Len {
		return nil, Error("invalid graph program")
	}
	if math.IsNaN(program.GlideMS) || math.IsInf(program.GlideMS, 0) || program.GlideMS < 0 {
		return nil, Error("invalid graph glide time")
	}
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("unsupported sample rate")
	}
	for i := 0; i < int(program.Len); i++ {
		n := program.Nodes[i]
		var inputs int
		switch n.Op {
		case Pitch, Gate, Velocity, SampleRate, Constant, Noise:
		case Saw, Square, Sine, Tanh, Exp2:
			inputs = 1
		case Add, Subtract, Multiply, Divide, Envelope, Lowpass, Highpass, Pulse:
			inputs = 2
		case Ladder, Diode, Mix, Clamp, SVF:
			inputs = 3
		case ADSR:
			inputs = 5
		default:
			return nil, Error("unknown graph operation")
		}
		if (inputs > 0 && int(n.A) >= i) || (inputs > 1 && int(n.B) >= i) || (inputs > 2 && int(n.C) >= i) || (inputs > 3 && int(n.D) >= i) || (inputs > 4 && int(n.E) >= i) {
			return nil, Error("graph input must precede its node")
		}
		if n.Op == Constant && (math.IsNaN(float64(n.Value)) || math.IsInf(float64(n.Value), 0)) {
			return nil, Error("non-finite graph constant")
		}
	}
	v := new(Voice)
	v.program = program
	v.sampleRate = float32(sampleRate)
	if usesQuality(program) {
		v.quality = newQualityState(program, sampleRate)
		v.sampleRate *= 2
	}
	if program.GlideMS > 0 {
		v.pitchAlpha = 1 - math.Exp(-1/(program.GlideMS/1000*float64(v.sampleRate)))
	}
	for i := 0; i < int(program.Len); i++ {
		v.states[i].noise = uint32(i+1)*0x9e3779b9 ^ 0xa5a5a5a5
	}
	return v, nil
}

func (v *Voice) NoteOn(note, velocity uint8, slide bool) {
	wasGated := v.gate > 0
	targetLog := math.Log2(440) + (float64(note)-69)/12
	v.velocity = float32(velocity) / 127
	v.gate = 1
	if slide && wasGated && v.pitch != 0 {
		if v.pitchAlpha > 0 {
			v.targetLog = targetLog
			v.gliding = targetLog != v.pitchLog
			return
		}
		v.pitch = float32(440 * math.Exp2((float64(note)-69)/12))
		v.pitchLog, v.targetLog = targetLog, targetLog
		v.gliding = false
		return
	}
	v.pitch = float32(440 * math.Exp2((float64(note)-69)/12))
	v.pitchLog, v.targetLog = targetLog, targetLog
	v.gliding = false
	if v.quality != nil {
		v.quality.retrigger()
	}
	for i := 0; i < int(v.program.Len); i++ {
		s := &v.states[i]
		switch v.program.Nodes[i].Op {
		case Saw, Square, Sine:
			s.phase = 0
		case Envelope:
			s.env = 1
		}
	}
}

func (v *Voice) NoteOff() { v.gate = 0 }

func (v *Voice) Reset() {
	v.pitch, v.gate, v.velocity = 0, 0, 0
	v.pitchLog, v.targetLog, v.gliding = 0, 0, false
	if v.quality != nil {
		v.quality.reset()
	}
	for i := 0; i < int(v.program.Len); i++ {
		v.values[i] = 0
		v.states[i].phase = 0
		v.states[i].env = 0
		v.states[i].filter = [4]float32{}
	}
}

func (v *Voice) Next() float32 {
	if v.quality == nil {
		return v.nextSample()
	}
	first, second := v.nextSample(), v.nextSample()
	out := float32(v.quality.down.Downsample(float64(first), float64(second)))
	if v.quality.fade > 0 {
		blend := float32(v.quality.fade) / float32(v.quality.fadeFrames)
		out = out*(1-blend) + v.quality.previous*blend
		v.quality.fade--
	}
	v.quality.last = out
	return out
}

//go:noinline
func (v *Voice) nextSample() float32 {
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
			if v.quality != nil {
				y = v.quality.oscillate(i, n.Op, a, 0.5, v.sampleRate)
				break
			}
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
		case Pulse:
			y = v.quality.oscillate(i, Pulse, a, b, v.sampleRate)
		case ADSR:
			y = v.quality.nodes[i].envelope.next(a > 0, b, c, v.values[n.D], v.values[n.E], v.sampleRate)
		case SVF:
			y = v.quality.nodes[i].filter.next(a, b, c, v.sampleRate, v.quality.baseRate)
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

func clamp(x, lo, hi float32) float32 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func frac(x float32) float32 {
	if x >= 1 {
		return x - 1
	}
	return x
}

func polyBLEP(phase, dt float32) float32 {
	if phase < dt {
		t := phase / dt
		return t + t - t*t - 1
	}
	if phase > 1-dt {
		t := (phase - 1) / dt
		return t*t + t + t + 1
	}
	return 0
}
