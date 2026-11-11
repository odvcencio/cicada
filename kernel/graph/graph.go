// Package graph is a fixed-size, pure-Go DSP graph executor. Programs are
// compiled and validated outside the audio callback.
package graph

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/detmath"

	"m31labs.dev/cicada/kernel/amp"
	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/modulate"
	"m31labs.dev/cicada/kernel/voice/ddsp"
)

const MaxNodes = 128
const MaxModulators = 32

// Each delay primitive reserves a full ring before rendering. The core
// profile admits two rings per voice, including unused graph bindings.
const MaxDelaySamples = 4096
const MaxVoiceDelaySamples = 8192

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
	// 23 is reserved for concurrent graph expansion. Command opcodes are a
	// separate namespace (the chord command in image version 14 uses 22).
	Delay     Op = 24
	Comb      Op = 25
	Period    Op = 26 // unit / Hz, converted from seconds to milliseconds
	PitchBend Op = 27 // pitch cents; zero is neutral
	NeuralAmp Op = 28 // pinned quantized causal amp: audio, unit drive
	PM        Op = 32 // sine carrier with an audio phase offset scaled in radians
	ADSR      Op = 23
	Pulse     Op = 33
	SVF       Op = 34
	Modulator Op = 35 // shared, control-grid-held project source
	LFO       Op = 36 // per-sample, song- or note-locked oscillator
	DDSP      Op = 31 // harmonic-plus-noise reed: fundamental Hz, linear loudness
	Pressure  Op = 29 // normalized 0..1; zero is neutral
	Timbre    Op = 30 // normalized 0..1; 0.5 is neutral
)

type Node struct {
	Op    Op
	A     uint8
	B     uint8
	C     uint8
	D     uint8 `json:",omitempty"`
	E     uint8 `json:",omitempty"`
	Value float32
	// Comb stores its fourth input index in Value, preserving the 8-byte image
	// node record. Other operations retain their existing Value semantics.
}

type Program struct {
	Nodes   [MaxNodes]Node
	Len     uint8
	Output  uint8
	GlideMS float64                   // portamento time constant in milliseconds; zero keeps immediate pitch changes
	Vibrato *expression.VibratoConfig `json:",omitempty"`
}

type Error string

func (e Error) Error() string { return string(e) }

type nodeState struct {
	phase  float32
	env    float32
	filter [4]float32
	noise  uint32
	delay  uint8
	amp    uint8
	neural uint8

	// Each coefficient input is clamped above zero; zero marks an empty cache.
	// The program and sample rate stay fixed for the lifetime of the voice.
	coefficientInput float32
	coefficient      float32
}

// Voice owns all state and storage for one instance of a program. Next does
// not allocate, lock, or use goroutines.
type Voice struct {
	program     Program
	modulators  *[MaxModulators]float32
	values      [MaxNodes]float32
	states      [MaxNodes]nodeState
	sampleRate  float32
	pitch       float32
	pitchLog    float64
	targetLog   float64
	pitchAlpha  float64
	gliding     bool
	gate        float32
	velocity    float32
	quality     *qualityState
	expression  expression.State
	delays      []delayState
	delayMemory []float32
	amps        []amp.Model
	ddspVoices  []ddsp.Synth
	lfo         *lfoVoice
}

//go:noinline
func NewVoice(program Program, sampleRate int) (*Voice, error) {
	return NewVoiceFromProgram(&program, sampleRate)
}

// NewVoiceFromProgram prepares voice storage without copying the input program at each call.
// The voice owns its program after construction.
//
//go:noinline
func NewVoiceFromProgram(program *Program, sampleRate int) (*Voice, error) {
	return NewVoiceFromProgramWithModulators(program, sampleRate, nil)
}

// NewVoiceFromProgramWithModulators binds project control values for this voice.
func NewVoiceFromProgramWithModulators(program *Program, sampleRate int, modulators *[MaxModulators]float32) (*Voice, error) {
	if err := ValidateProgram(program, sampleRate); err != nil {
		return nil, err
	}
	if instrumentVibratoEnabled && modulators == nil {
		for _, node := range program.Nodes[:program.Len] {
			if node.Op == Modulator {
				return nil, validationError("graph modulator has no source binding")
			}
		}
	}
	v := new(Voice)
	v.program = *program
	v.modulators = modulators
	if instrumentVibratoEnabled {
		for i := range program.Nodes[:program.Len] {
			if program.Nodes[i].Op == LFO {
				if v.lfo == nil {
					v.lfo = new(lfoVoice)
				}
				v.states[i].neural = uint8(len(v.lfo.rates))
				v.lfo.rates = append(v.lfo.rates, uint64(float64(float64(program.Nodes[i].Value)*1_000_000)+0.5))
			}
		}
	}
	v.sampleRate = float32(sampleRate)
	if usesQuality(program) {
		v.quality = newQualityState(program, sampleRate)
		v.sampleRate *= 2
	}
	if program.GlideMS > 0 {
		v.pitchAlpha = 1 - detmath.Exp(-1/(program.GlideMS/1000*float64(v.sampleRate)))
	}
	v.expression.Reset()
	if instrumentVibratoEnabled && program.Vibrato != nil {
		if err := v.expression.ConfigureVibrato(*program.Vibrato, v.sampleRate); err != nil {
			return nil, err
		}
	}
	count := delaySamples(program) / MaxDelaySamples
	if count > 0 {
		v.delayMemory = make([]float32, count*MaxDelaySamples)
		v.delays = make([]delayState, count)
	}
	count = 0
	for i := 0; i < int(program.Len); i++ {
		if program.Nodes[i].Op == DDSP {
			count++
		}
	}
	if count > 0 {
		v.ddspVoices = make([]ddsp.Synth, count)
	}
	count = 0
	neural := 0
	for i := 0; i < int(program.Len); i++ {
		v.states[i].noise = uint32(i+1)*0x9e3779b9 ^ 0xa5a5a5a5
		if program.Nodes[i].Op == Delay || program.Nodes[i].Op == Comb {
			v.states[i].delay = uint8(count)
			count++
		}
		if program.Nodes[i].Op == DDSP {
			v.states[i].neural = uint8(neural)
			v.ddspVoices[neural], _ = ddsp.New(sampleRate)
			neural++
		}
	}
	count = 0
	for i := 0; i < int(program.Len); i++ {
		if program.Nodes[i].Op == NeuralAmp {
			v.states[i].amp = uint8(count)
			count++
		}
	}
	if count > 0 {
		v.amps = make([]amp.Model, count)
	}
	return v, nil
}

// Validate checks untrusted images and statically known delay controls without
// allocating voice storage. Dynamic controls are bounded again in Next.
func Validate(program Program, sampleRate int) error {
	return ValidateProgram(&program, sampleRate)
}

// ValidateProgram checks a program without passing its fixed node array by value.
func ValidateProgram(program *Program, sampleRate int) error {
	if program.Vibrato != nil {
		if !instrumentVibratoEnabled {
			return validationError("instrument vibrato requires the modular kernel")
		}
		if err := program.Vibrato.Validate(); err != nil {
			return err
		}
	}
	if program.Len == 0 || int(program.Len) > MaxNodes || program.Output >= program.Len {
		return validationError("invalid graph program")
	}
	if math.IsNaN(program.GlideMS) || math.IsInf(program.GlideMS, 0) || program.GlideMS < 0 {
		return validationError("invalid graph glide time")
	}
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return validationError("unsupported sample rate")
	}
	if instrumentVibratoEnabled && HasLFO(program) && statefulNodeCount(program) > 32 {
		return validationError("voice graph exceeds 32 stateful nodes")
	}
	for i := 0; i < int(program.Len); i++ {
		n := program.Nodes[i]
		if int(n.A) >= MaxNodes || int(n.B) >= MaxNodes || int(n.C) >= MaxNodes {
			return validationError("invalid graph input index")
		}
		var inputs int
		switch n.Op {
		case Pitch, Gate, Velocity, SampleRate, Constant, Noise, PitchBend, Pressure, Timbre:
		case Modulator:
			if !instrumentVibratoEnabled || n.Value < 0 || n.Value >= MaxModulators || n.Value != float32(uint8(n.Value)) {
				return validationError("invalid or unavailable graph modulator")
			}
		case LFO:
			if !instrumentVibratoEnabled || n.A > 3 || n.B > 1 || n.C != 0 || n.D != 0 || n.E != 0 || math.IsNaN(float64(n.Value)) || math.IsInf(float64(n.Value), 0) || n.Value < .01 || n.Value > 20 {
				return validationError("invalid or unavailable graph LFO")
			}
		case Saw, Square, Sine, Tanh, Exp2:
			inputs = 1
		case Add, Subtract, Multiply, Divide, Period, Envelope, Lowpass, Highpass, Delay, NeuralAmp, Pulse, DDSP:
			inputs = 2
		case Ladder, Diode, Mix, Clamp, PM, SVF:
			inputs = 3
		case ADSR:
			inputs = 5
		case Comb:
			inputs = 3
			if n.Value < 0 || n.Value >= float32(i) || n.Value != float32(uint8(n.Value)) {
				return validationError("invalid comb damping input")
			}
		default:
			return validationError("unknown graph operation")
		}
		if (inputs > 0 && int(n.A) >= i) || (inputs > 1 && int(n.B) >= i) || (inputs > 2 && int(n.C) >= i) || (inputs > 3 && int(n.D) >= i) || (inputs > 4 && int(n.E) >= i) {
			return validationError("graph input must precede its node")
		}
		if n.Op == Constant && (math.IsNaN(float64(n.Value)) || math.IsInf(float64(n.Value), 0)) {
			return validationError("non-finite graph constant")
		}
	}
	if delaySamples(program) > MaxVoiceDelaySamples {
		return validationError("voice graph exceeds 8192 delay samples (two delay nodes)")
	}
	return validateDelayPitch(program, sampleRate, 0)
}

// ValidateModulatorIndices checks bindings against the actual source table.
func ValidateModulatorIndices(program *Program, count int) error {
	if program == nil || int(program.Len) > MaxNodes {
		return validationError("invalid graph program")
	}
	for _, node := range program.Nodes[:program.Len] {
		if node.Op == Modulator && (!instrumentVibratoEnabled || node.Value < 0 || node.Value >= float32(count) || node.Value != float32(uint8(node.Value))) {
			return validationError("graph modulator source is unavailable")
		}
	}
	return nil
}

func (v *Voice) NoteOn(note, velocity uint8, slide bool) {
	v.expression.Reset()
	wasGated := v.gate > 0
	targetLog := detmath.Log2(440) + (float64(note)-69)/12
	v.velocity = float32(velocity) / 127
	v.gate = 1
	if slide && wasGated && v.pitch != 0 {
		if v.pitchAlpha > 0 {
			v.targetLog = targetLog
			v.gliding = targetLog != v.pitchLog
			return
		}
		v.pitch = float32(440 * detmath.Exp2((float64(note)-69)/12))
		v.pitchLog, v.targetLog = targetLog, targetLog
		v.gliding = false
		return
	}
	v.pitch = float32(440 * detmath.Exp2((float64(note)-69)/12))
	v.pitchLog, v.targetLog = targetLog, targetLog
	v.gliding = false
	if instrumentVibratoEnabled && v.lfo != nil {
		v.lfo.noteSample = 0
	}
	if v.quality != nil {
		v.quality.retrigger()
	}
	for i := 0; i < int(v.program.Len); i++ {
		s := &v.states[i]
		switch v.program.Nodes[i].Op {
		case Saw, Square, Sine, PM:
			s.phase = 0
		case Envelope:
			s.env = 1
		case DDSP:
			v.ddspVoices[s.neural].Reset()
		}
	}
}

// SetExpression applies resolved score controls without retriggering the voice.
func (v *Voice) SetExpression(p expression.Params) { v.expression.SetParams(p, v.sampleRate) }

func (v *Voice) SetGroupControls(c expression.Controls) { v.expression.SetGroupControls(c) }

// NoteExpression updates the three live controls, preserving vibrato phase.
func (v *Voice) NoteExpression(pitchCents, pressure, timbre float32) {
	v.expression.SetControls(pitchCents, pressure, timbre)
}

func (v *Voice) NoteOff() { v.gate = 0 }

// CopyStateFrom copies a voice constructed with the same program into this
// voice's preallocated storage. Neither delay state nor ring memory is shared.
func (v *Voice) CopyStateFrom(source *Voice) {
	delays, memory, amps, neural := v.delays, v.delayMemory, v.amps, v.ddspVoices
	quality, modulators, lfo := v.quality, v.modulators, v.lfo
	*v = *source
	v.modulators = modulators
	v.lfo = lfo
	if instrumentVibratoEnabled && lfo != nil {
		clock, rates := lfo.clock, lfo.rates
		*lfo = *source.lfo
		lfo.clock, lfo.rates = clock, rates
		copy(lfo.rates, source.lfo.rates)
	}
	v.delays, v.delayMemory, v.amps = delays, memory, amps
	copy(v.delays, source.delays)
	copy(v.delayMemory, source.delayMemory)
	copy(v.amps, source.amps)
	v.ddspVoices = neural
	copy(v.ddspVoices, source.ddspVoices)
	v.quality = quality
	if quality != nil {
		quality.copyFrom(source.quality)
	}
}

func (v *Voice) Reset() {
	v.expression.Reset()
	v.pitch, v.gate, v.velocity = 0, 0, 0
	v.pitchLog, v.targetLog, v.gliding = 0, 0, false
	if instrumentVibratoEnabled && v.lfo != nil {
		v.lfo.noteSample, v.lfo.localSample = 0, 0
	}
	if v.quality != nil {
		v.quality.reset()
	}
	for i := uint8(0); i < v.program.Len; i++ {
		index := i & (MaxNodes - 1)
		v.values[index] = 0
		v.states[index].phase = 0
		v.states[index].env = 0
		v.states[index].filter = [4]float32{}
	}
	clear(v.delayMemory)
	clear(v.delays)
	clear(v.amps)
	for i := range v.ddspVoices {
		v.ddspVoices[i].Reset()
	}
}

func (v *Voice) Next() float32 {
	if v.quality == nil {
		return v.nextSample(0)
	}
	first, second := v.nextSample(0), v.nextSample(1)
	out := float32(v.quality.down.Downsample(float64(first), float64(second)))
	if v.quality.fade > 0 {
		blend := float32(v.quality.fade) / float32(v.quality.fadeFrames)
		out = float32(out*(1-blend)) + float32(v.quality.previous*blend)
		v.quality.fade--
	}
	v.quality.last = out
	return out
}

//go:noinline
func (v *Voice) nextSample(substep uint64) float32 {
	if v.gliding {
		v.pitchLog += float64((v.targetLog - v.pitchLog) * v.pitchAlpha)
		v.pitch = float32(fastmath.Exp2(v.pitchLog))
	}
	pitch := v.pitch
	if ratio := v.expression.NextRatio(); ratio != 1 {
		pitch *= float32(ratio)
	}
	// NewVoice validates the 128-node limit and all used input indices.
	for i := uint8(0); i < v.program.Len; i++ {
		index := i & (MaxNodes - 1)
		n := v.program.Nodes[index]
		s := &v.states[index]
		a, b, c := v.values[n.A&(MaxNodes-1)], v.values[n.B&(MaxNodes-1)], v.values[n.C&(MaxNodes-1)]
		var y float32
		switch n.Op {
		case Pitch:
			y = pitch
		case PitchBend:
			y = v.expression.PitchCents
		case Pressure:
			y = v.expression.Pressure
		case Timbre:
			y = v.expression.Timbre
		case Gate:
			y = v.gate
		case Velocity:
			y = v.velocity
		case SampleRate:
			y = v.sampleRate
		case Constant:
			y = n.Value
		case Modulator:
			if instrumentVibratoEnabled {
				y = v.modulators[uint8(n.Value)]
			}
		case LFO:
			if instrumentVibratoEnabled {
				lfo := v.lfo
				sample, seed := lfo.noteSample, uint32(0)
				if lfo.clock != nil {
					seed = lfo.clock.Seed
				}
				if n.B == 0 {
					sample = lfo.localSample
					if lfo.clock != nil {
						sample = lfo.clock.Sample
						if v.quality != nil {
							sample *= 2
							if lfo.clock.Playing {
								sample += substep
							}
						}
					}
				}
				position := modulate.FreePosition(sample, uint32(v.sampleRate), lfo.rates[s.neural])
				y = float32(modulate.LFO(modulate.Wave(n.A), seed, index, position)) / modulate.One
			}
		case DDSP:
			y = v.ddspVoices[s.neural].NextFloat(a, b)
		case Add:
			y = a + b
		case Subtract:
			y = a - b
		case Multiply:
			y = a * b
		case Divide, Period:
			if b != 0 {
				y = a / b
				if n.Op == Period {
					y *= 1000
				}
			}
		case Delay, Comb:
			d := &v.delays[s.delay]
			ring := v.delayMemory[int(s.delay)*MaxDelaySamples : (int(s.delay)+1)*MaxDelaySamples]
			if n.Op == Delay {
				y = d.linear(ring, a, finiteClamp(b*v.sampleRate/1000, 1, MaxDelaySamples))
			} else {
				y = d.comb(ring, a, finiteClamp(b*v.sampleRate/1000, 4, MaxDelaySamples), finiteClamp(c, 0, .99999994), finiteClamp(v.values[uint8(n.Value)], 0, .99999994))
			}
		case NeuralAmp:
			y = v.amps[s.amp].ProcessFloat(a, b)
		case Saw, Square, Sine, PM:
			if n.Op != PM && v.quality != nil {
				y = v.quality.oscillate(int(index), n.Op, a, 0.5, v.sampleRate)
				break
			}
			frequency := clamp(a, 0, v.sampleRate*0.49)
			if n.Op == PM {
				frequency = finiteClamp(a, 0, v.sampleRate*0.49)
			}
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
				case Sine, PM:
					// Only the carrier advances stored phase. Reduce the offset
					// in float64 so finite audio/index products cannot overflow
					// or grow the sine argument. Sidebands are not band-limited.
					offset := 0.0
					if n.Op == PM {
						offset = float64(b) * float64(c) / (2 * math.Pi)
						offset -= math.Floor(offset)
					}
					if !math.IsNaN(offset) {
						y = float32(detmath.SinPi(2 * (float64(phase) + offset)))
					}
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
			if ms != s.coefficientInput {
				s.coefficient = float32(detmath.Exp(-4600 / float64(ms*v.sampleRate)))
				s.coefficientInput = ms
			}
			s.env *= s.coefficient
		case Pulse:
			y = v.quality.oscillate(int(index), Pulse, a, b, v.sampleRate)
		case ADSR:
			y = v.quality.nodes[i].envelope.next(a > 0, b, c, v.values[n.D], v.values[n.E], v.sampleRate)
		case SVF:
			y = v.quality.nodes[i].filter.next(a, b, c, v.sampleRate, v.quality.baseRate)
		case Ladder, Diode:
			frequency := clamp(b, 20, v.sampleRate*0.45)
			if frequency != s.coefficientInput {
				g := float32(detmath.Tan(math.Pi * float64(frequency/v.sampleRate)))
				s.coefficient = g / (1 + g)
				s.coefficientInput = frequency
			}
			coef := s.coefficient
			feedback := float32(3.2) * clamp(c, 0, 1)
			if n.Op == Diode {
				feedback = 2.8 * clamp(c, 0, 1)
			}
			y = ladder(&s.filter, coef, feedback, a)
		case Lowpass, Highpass:
			frequency := clamp(b, 20, v.sampleRate*0.45)
			if frequency != s.coefficientInput {
				s.coefficient = float32(1 - detmath.Exp(-2*math.Pi*float64(frequency/v.sampleRate)))
				s.coefficientInput = frequency
			}
			coef := s.coefficient
			s.filter[0] += float32(coef * (a - s.filter[0]))
			y = s.filter[0]
			if n.Op == Highpass {
				y = a - y
			}
		case Mix:
			blend := clamp(c, 0, 1)
			y = float32(a*(1-blend)) + float32(b*blend)
		case Tanh:
			y = float32(detmath.Tanh(float64(a)))
		case Exp2:
			y = float32(detmath.Exp2(float64(clamp(a, -8, 8))))
		case Clamp:
			y = clamp(a, b, c)
		}
		v.values[index] = y
	}
	if instrumentVibratoEnabled && v.lfo != nil {
		v.lfo.noteSample++
		v.lfo.localSample++
	}
	out := v.values[v.program.Output&(MaxNodes-1)]
	if math.IsNaN(float64(out)) || math.IsInf(float64(out), 0) {
		v.Reset()
		return 0
	}
	return out
}

// ladderInputLimit bounds a ladder's input. With feedback at most 3.2 the
// tanh argument is at least a third of the input, far past the point where a
// float32 tanh is exactly 1.
const ladderInputLimit = 1e4

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
		return t + t - float32(t*t) - 1
	}
	if phase > 1-dt {
		t := (phase - 1) / dt
		return float32(t*t) + t + t + 1
	}
	return 0
}

// ladder runs one sample of a four-stage ladder: trapezoidal (zero-delay)
// one-pole stages whose corners sit at the cutoff, with the resonance feedback
// solved within the sample. As in an analog ladder, the cutoff is the stage
// corner: without resonance the response is 3 dB down at about 0.435 times the
// cutoff, and the resonant peak sits just below the cutoff (0.92 times it at
// resonance 0.9) and approaches it as resonance rises, at every cutoff. coef
// is g/(1+g) with g = tan(pi*cutoff/rate).
//
// Each stage is out = coef*in + (1-coef)*state, so the output is
// coef^4*in + S, where S collects the stage states. Solving
// y = coef^4*(x - k*y) + S for y gives the feedback estimate; the input then
// saturates through tanh. Products round to float32 before additions so arm64
// cannot contract an FMA and results stay identical across targets.
//
// The input is held to +-1e4 so an infinite sample cannot turn the estimate
// into Inf - Inf and leave NaN in the stage states. tanh already returns
// exactly +-1 for any input that large, so no finite render changes.
func ladder(state *[4]float32, coef, feedback, x float32) float32 {
	x = clamp(x, -ladderInputLimit, ladderInputLimit)
	rest := 1 - coef
	g2 := float32(coef * coef)
	g3 := float32(g2 * coef)
	g4 := float32(g3 * coef)
	stateSum := float32(float32(g3*rest)*state[0]) + float32(float32(g2*rest)*state[1]) +
		float32(float32(coef*rest)*state[2]) + float32(rest*state[3])
	estimate := (float32(g4*x) + stateSum) / (1 + float32(feedback*g4))
	input := float32(detmath.Tanh(float64(x - float32(feedback*estimate))))
	for stage := 0; stage < 4; stage++ {
		step := float32(coef * (input - state[stage]))
		out := step + state[stage]
		state[stage] = out + step
		input = out
	}
	return input
}
