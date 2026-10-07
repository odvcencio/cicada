// Package polykeys implements eight-voice analog-style keys and pads. Oscillators
// and filters run at twice the host rate; chorus runs at the host rate.
package polykeys

import "math"

const MaxVoices = 8

type Error string

func (e Error) Error() string { return string(e) }

type Filter uint8

const (
	Ladder Filter = iota
	StateVariable
)

// Params are immutable after New. Times are seconds, Cutoff is Hz, Detune and
// Drift are cents, Sync is the slave/master frequency ratio, and LevelDB is dB.
type Params struct {
	Saw, Pulse, PulseWidth, PWM, PWMRate, Sub, Detune, Drift, Sync float64
	Cutoff, Resonance, KeyTrack, FilterEnv, Drive                  float64
	Attack, Decay, Sustain, Release, Velocity, Chorus, LevelDB     float64
	Filter                                                         Filter
}

func DefaultParams() Params {
	return Params{Saw: .7, Pulse: .3, PulseWidth: .45, PWM: .12, PWMRate: .41,
		Sub: .16, Detune: 7, Drift: 2, Sync: 1, Cutoff: 2400, Resonance: .18,
		KeyTrack: .45, FilterEnv: 2.4, Drive: .12, Attack: .008, Decay: .7,
		Sustain: .55, Release: .3, Velocity: .75, Chorus: .28, LevelDB: -9, Filter: Ladder}
}

// Patch returns original designs, rather than imported factory patch data.
func Patch(name string) (Params, error) {
	p := DefaultParams()
	switch name {
	case "poly_keys":
	case "brass_stab":
		p.Saw, p.Pulse, p.PWM, p.Sub = 1, .15, .04, .25
		p.Cutoff, p.FilterEnv, p.Resonance = 850, 3.8, .24
		p.Attack, p.Decay, p.Sustain, p.Release = .015, .32, .48, .14
		p.Chorus, p.Drive = .16, .22
	case "soft_pad":
		p.Saw, p.Pulse, p.PWM, p.Detune = .45, .7, .24, 9
		p.Cutoff, p.FilterEnv, p.Resonance = 1450, 1.2, .12
		p.Attack, p.Decay, p.Sustain, p.Release = .65, 1.8, .82, 1.6
		p.Chorus, p.Velocity, p.LevelDB = .65, .35, -11
	case "sync_lead":
		p.Saw, p.Pulse, p.Sub, p.Sync = 1, 0, .14, 2.75
		p.Cutoff, p.FilterEnv, p.Resonance, p.Filter = 1800, 2.5, .42, StateVariable
		p.Attack, p.Decay, p.Sustain, p.Release = .004, .3, .65, .16
		p.Chorus, p.Drive = .08, .28
	default:
		return Params{}, Error("unknown poly keys patch")
	}
	return p, nil
}

func (p Params) Validate() error {
	values := [...]float64{p.Saw, p.Pulse, p.PulseWidth, p.PWM, p.PWMRate, p.Sub, p.Detune, p.Drift, p.Sync, p.Cutoff, p.Resonance, p.KeyTrack, p.FilterEnv, p.Drive, p.Attack, p.Decay, p.Sustain, p.Release, p.Velocity, p.Chorus, p.LevelDB}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Error("poly keys parameters must be finite")
		}
	}
	if p.Saw < 0 || p.Saw > 1 || p.Pulse < 0 || p.Pulse > 1 || p.Saw+p.Pulse == 0 || p.PulseWidth < .1 || p.PulseWidth > .9 || p.PWM < 0 || p.PWM > .35 || p.PWMRate < .01 || p.PWMRate > 10 || p.Sub < 0 || p.Sub > 1 || p.Detune < 0 || p.Detune > 30 || p.Drift < 0 || p.Drift > 10 || p.Sync < 1 || p.Sync > 8 {
		return Error("poly keys oscillator parameter is out of range")
	}
	if p.Cutoff < 20 || p.Cutoff > 18000 || p.Resonance < 0 || p.Resonance > .95 || p.KeyTrack < 0 || p.KeyTrack > 1 || p.FilterEnv < 0 || p.FilterEnv > 6 || p.Drive < 0 || p.Drive > 1 || p.Filter > StateVariable {
		return Error("poly keys filter parameter is out of range")
	}
	if p.Attack < .001 || p.Attack > 10 || p.Decay < .01 || p.Decay > 10 || p.Sustain < 0 || p.Sustain > 1 || p.Release < .01 || p.Release > 10 || p.Velocity < 0 || p.Velocity > 1 || p.Chorus < 0 || p.Chorus > 1 || p.LevelDB < -60 || p.LevelDB > 6 {
		return Error("poly keys envelope or output parameter is out of range")
	}
	return nil
}

type coefficients struct {
	ladder, a1, a2, a3 float32
}

type voice struct {
	phase, phaseB, subPhase, syncCorrection float32
	delta, deltaB, drift, driftPhase        float32
	amp, velocity, cutoffIndex              float32
	last, tail, fade                        float32
	poles                                   [4]float32
	ic1, ic2                                float32
	note                                    uint8
	stage                                   uint8
	held                                    bool
	age                                     uint64
}

// Instrument owns all buffers. New may allocate; triggering and rendering do not.
type Instrument struct {
	params                          Params
	voices                          [8]voice
	deltas, cutoffIndexes           [88]float32
	detuneRatios, driftAmounts      [8]float32
	driftRates, panL, panR          [8]float32
	table                           [257]coefficients
	sine                            [1025]float32
	delay                           [8192]float32
	chorusPhases, chorusRates       [3]float32
	chorusLP                        [2]float32
	dcInput, dcOutput               [2]float32
	dcAlpha                         float32
	write                           int
	voiceLimit                      int
	clock                           uint64
	attack, decay, release, sustain float32
	waveSaw, wavePulse, width, pwm  float32
	pwmPhase, pwmRate, sub, sync    float32
	drive, gain, velocity, envIndex float32
	resonanceFeedback               float32
	chorus, chorusBase, chorusDepth float32
	chorusInput, chorusOutput       float32
	sustainPedal                    bool
}

func New(rate int, params Params) (*Instrument, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 && rate != 192000 {
		return nil, Error("poly keys sample rate must be 44100, 48000, 96000, or 192000")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	i := &Instrument{params: params, voiceLimit: MaxVoices}
	sr := float64(rate)
	i.attack = float32(1 / (params.Attack * sr))
	i.decay = float32(math.Exp(-4.605170186 / (params.Decay * sr)))
	i.release = float32(math.Exp(-4.605170186 / (params.Release * sr)))
	i.sustain = float32(params.Sustain)
	norm := 1 / (params.Saw + params.Pulse + .5*params.Sub)
	i.waveSaw, i.wavePulse = float32(params.Saw*norm), float32(params.Pulse*norm)
	i.sub, i.width, i.pwm = float32(.5*params.Sub*norm), float32(params.PulseWidth), float32(params.PWM)
	i.pwmRate, i.sync = float32(params.PWMRate/sr), float32(params.Sync)
	i.drive, i.gain, i.velocity = float32(1+4*params.Drive), float32(.4*math.Pow(10, params.LevelDB/20)), float32(params.Velocity)
	i.resonanceFeedback = float32(3.8 * params.Resonance)
	ceiling := math.Min(18000, sr*.2)
	span := math.Log2(ceiling / 20)
	i.envIndex = float32(params.FilterEnv * 256 / span)
	for n := range i.deltas {
		freq := 440 * math.Pow(2, (float64(n+21)-69)/12)
		i.deltas[n] = float32(freq / (2 * sr))
		cutoff := params.Cutoff * math.Pow(2, params.KeyTrack*(float64(n+21)-60)/12)
		i.cutoffIndexes[n] = float32(math.Log2(cutoff/20) * 256 / span)
	}
	for t := range i.table {
		cutoff := 20 * math.Pow(2, float64(t)*span/256)
		g := math.Tan(math.Pi * cutoff / (2 * sr))
		gl := g / .43497944204608224
		k := math.Sqrt2 * (1 - .95*params.Resonance)
		a1 := 1 / (1 + g*(g+k))
		i.table[t] = coefficients{float32(gl / (1 + gl)), float32(a1), float32(g * a1), float32(g * g * a1)}
	}
	seed := uint32(0x9e3779b9)
	for v := range i.voices {
		seed = seed*1664525 + 1013904223
		signed := float64(seed>>8)/8388607.5 - 1
		i.detuneRatios[v] = float32(math.Pow(2, (params.Detune+signed*params.Drift)/1200))
		i.driftAmounts[v] = float32(params.Drift * .00057762265)
		i.driftRates[v] = float32((.071 + float64(v)*.013) / sr)
		pan := float64(v)/7*.6 - .3
		i.panL[v], i.panR[v] = float32(math.Sqrt((1-pan)*.5)), float32(math.Sqrt((1+pan)*.5))
	}
	for s := range i.sine {
		i.sine[s] = float32(math.Sin(2 * math.Pi * float64(s) / 1024))
	}
	i.chorus = float32(params.Chorus)
	i.dcAlpha = float32(math.Exp(-2 * math.Pi * 12 / sr))
	i.chorusBase, i.chorusDepth = float32(sr*.013), float32(sr*.0034)
	i.chorusInput, i.chorusOutput = float32(1-math.Exp(-2*math.Pi*8000/sr)), float32(1-math.Exp(-2*math.Pi*6500/sr))
	for c := range i.chorusPhases {
		i.chorusPhases[c] = float32(c) / 3
		i.chorusRates[c] = float32((.31 + float64(c)*.137) / sr)
	}
	return i, nil
}

func (i *Instrument) NoteOn(note, velocity uint8) error {
	if note < 21 || note > 108 || velocity > 127 {
		return Error("poly keys note must be 21..108 and velocity 0..127")
	}
	if velocity == 0 {
		i.NoteOff(note)
		return nil
	}
	slot := -1
	for n := 0; n < i.voiceLimit; n++ {
		v := &i.voices[n]
		if v.stage != 0 && v.note == note {
			slot = n
			break
		}
		if v.stage == 0 && slot < 0 {
			slot = n
		}
	}
	if slot < 0 {
		slot = 0
		for n := 1; n < i.voiceLimit; n++ {
			a, b := &i.voices[n], &i.voices[slot]
			if (a.stage == 3 && b.stage != 3) || (a.stage == b.stage && a.age < b.age) || (a.stage != 3 && b.stage != 3 && a.age < b.age) {
				slot = n
			}
		}
	}
	i.clock++
	tail := i.voices[slot].last
	vel := float32(velocity) / 127
	i.voices[slot] = voice{note: note, held: true, stage: 1, age: i.clock,
		delta: i.deltas[note-21], deltaB: float32(i.deltas[note-21] * i.detuneRatios[slot]),
		phaseB: .317, driftPhase: float32(slot) * .117, velocity: float32(float32(1-i.velocity) + float32(i.velocity*vel)),
		cutoffIndex: i.cutoffIndexes[note-21], tail: tail}
	return nil
}

// SetVoiceLimit sets polyphony to 1..8 without allocating. Reducing the limit
// immediately clears voices outside it; Reset preserves the configured limit.
func (i *Instrument) SetVoiceLimit(count int) error {
	if count < 1 || count > MaxVoices {
		return Error("poly keys voice limit must be 1..8")
	}
	for n := count; n < len(i.voices); n++ {
		i.voices[n] = voice{}
	}
	i.voiceLimit = count
	return nil
}

func (i *Instrument) NoteOff(note uint8) {
	for n := range i.voices {
		v := &i.voices[n]
		if v.stage != 0 && v.note == note {
			v.held = false
			if !i.sustainPedal {
				v.stage = 3
			}
		}
	}
}

func (i *Instrument) AllNotesOff() {
	i.sustainPedal = false
	for n := range i.voices {
		if i.voices[n].stage != 0 {
			i.voices[n].held, i.voices[n].stage = false, 3
		}
	}
}

func (i *Instrument) SetSustain(value float32) error {
	if value != value || value < 0 || value > 1 {
		return Error("poly keys sustain pedal must be 0..1")
	}
	i.sustainPedal = value >= .5
	if !i.sustainPedal {
		for n := range i.voices {
			if i.voices[n].stage != 0 && !i.voices[n].held {
				i.voices[n].stage = 3
			}
		}
	}
	return nil
}

func (i *Instrument) Active() bool {
	for n := range i.voices {
		if i.voices[n].stage != 0 {
			return true
		}
	}
	return false
}

// Reset immediately silences notes and delay tails while keeping prepared data.
func (i *Instrument) Reset() {
	clear(i.voices[:])
	clear(i.delay[:])
	i.chorusLP = [2]float32{}
	i.dcInput, i.dcOutput = [2]float32{}, [2]float32{}
	i.write, i.clock, i.pwmPhase, i.sustainPedal = 0, 0, 0, false
	for c := range i.chorusPhases {
		i.chorusPhases[c] = float32(c) / 3
	}
}

func (i *Instrument) sin(phase float32) float32 {
	x := float32(phase * 1024)
	n := int(x)
	f := float32(x - float32(n))
	return float32(i.sine[n] + float32(f*float32(i.sine[n+1]-i.sine[n])))
}

func wrap(x float32) float32 { return float32(x - float32(int(x))) }

func blep(phase, delta float32) float32 {
	if phase < delta {
		x := float32(phase / delta)
		return float32(float32(x+x) - float32(x*x) - 1)
	}
	if phase > float32(1-delta) {
		x := float32(float32(phase-1) / delta)
		return float32(float32(x*x) + float32(x+x) + 1)
	}
	return 0
}

func saw(phase, delta float32) float32 {
	return float32(float32(2*phase-1) - blep(phase, delta))
}

func pulse(phase, delta, width float32) float32 {
	x := float32(-1)
	if phase < width {
		x = 1
	}
	p := float32(phase - width)
	if p < 0 {
		p = float32(p + 1)
	}
	x = float32(x + blep(phase, delta))
	return float32(x - blep(p, delta))
}

func saturate(x float32) float32 {
	if x > 3 {
		return 1
	}
	if x < -3 {
		return -1
	}
	x2 := float32(x * x)
	return float32(float32(x*float32(27+x2)) / float32(27+float32(9*x2)))
}

func (i *Instrument) filter(v *voice, x float32, c coefficients) float32 {
	x = saturate(float32(x * i.drive))
	if i.params.Filter == StateVariable {
		v3 := float32(x - v.ic2)
		v1 := float32(float32(c.a1*v.ic1) + float32(c.a2*v3))
		v2 := float32(float32(v.ic2+float32(c.a2*v.ic1)) + float32(c.a3*v3))
		v.ic1, v.ic2 = float32(float32(2*v1)-v.ic1), float32(float32(2*v2)-v.ic2)
		return v2
	}
	g := c.ladder
	f := float32(1 - g)
	g2 := float32(g * g)
	g3 := float32(g2 * g)
	g4 := float32(g3 * g)
	s := float32(float32(g3*v.poles[0]) + float32(g2*v.poles[1]))
	s = float32(s + float32(g*v.poles[2]) + v.poles[3])
	s = float32(s * f)
	k := i.resonanceFeedback
	x = float32(float32(x-float32(k*s)) / float32(1+float32(k*g4)))
	for p := range v.poles {
		d := float32(float32(x-v.poles[p]) * g)
		x = float32(d + v.poles[p])
		v.poles[p] = float32(x + d)
	}
	return float32(x * float32(1+float32(k*.2)))
}

func (i *Instrument) oscillator(v *voice, delta, width float32) float32 {
	deltaB := float32(v.deltaB * float32(1+v.drift))
	x := saw(v.phase, delta)
	if i.sync > 1 {
		deltaB = float32(delta * i.sync)
		if deltaB > .45 {
			deltaB = .45
		}
		x = float32(saw(v.phaseB, deltaB) + v.syncCorrection)
		v.syncCorrection = 0
		if v.phase >= float32(1-delta) {
			until := float32(float32(1-v.phase) / delta)
			resetPhase := wrap(float32(v.phaseB + float32(deltaB*until)))
			before := float32(1 - until)
			x = float32(x - float32(resetPhase*float32(before*before)))
			v.syncCorrection = float32(resetPhase * float32(until*until))
			v.phaseB = wrap(float32(deltaB * before))
		} else {
			v.phaseB = wrap(float32(v.phaseB + deltaB))
		}
	} else {
		x = float32(float32(.65*x) + float32(.35*saw(v.phaseB, deltaB)))
		v.phaseB = wrap(float32(v.phaseB + deltaB))
	}
	y := float32(i.waveSaw * x)
	if i.wavePulse > 0 {
		y = float32(y + float32(i.wavePulse*pulse(v.phase, delta, width)))
	}
	if i.sub > 0 {
		y = float32(y + float32(i.sub*pulse(v.subPhase, float32(delta*.5), .5)))
	}
	v.phase = wrap(float32(v.phase + delta))
	v.subPhase = wrap(float32(v.subPhase + float32(delta*.5)))
	return y
}

func (i *Instrument) NextStereo() (float32, float32) {
	i.pwmPhase = wrap(float32(i.pwmPhase + i.pwmRate))
	width := float32(i.width + float32(i.pwm*i.sin(i.pwmPhase)))
	if width < .08 {
		width = .08
	} else if width > .92 {
		width = .92
	}
	var left, right float32
	for n := range i.voices {
		v := &i.voices[n]
		if v.stage == 0 {
			continue
		}
		switch v.stage {
		case 1:
			v.amp = float32(v.amp + i.attack)
			if v.amp >= 1 {
				v.amp, v.stage = 1, 2
			}
		case 2:
			v.amp = float32(i.sustain + float32(float32(v.amp-i.sustain)*i.decay))
		case 3:
			v.amp = float32(v.amp * i.release)
			if v.amp < 1e-6 {
				v.stage, v.last = 0, 0
				continue
			}
		}
		v.driftPhase = wrap(float32(v.driftPhase + i.driftRates[n]))
		v.drift = float32(i.driftAmounts[n] * i.sin(v.driftPhase))
		delta := float32(v.delta * float32(1+v.drift))
		index := float32(v.cutoffIndex + float32(i.envIndex*float32(v.amp*v.velocity)))
		if index < 0 {
			index = 0
		} else if index > 255 {
			index = 255
		}
		idx, frac := int(index), float32(index-float32(int(index)))
		a, b := i.table[idx], i.table[idx+1]
		c := coefficients{float32(a.ladder + float32(frac*float32(b.ladder-a.ladder))), float32(a.a1 + float32(frac*float32(b.a1-a.a1))), float32(a.a2 + float32(frac*float32(b.a2-a.a2))), float32(a.a3 + float32(frac*float32(b.a3-a.a3)))}
		first := i.filter(v, i.oscillator(v, delta, width), c)
		second := i.filter(v, i.oscillator(v, delta, width), c)
		y := float32(float32(.5*float32(first+second)) * float32(v.amp*v.velocity))
		if v.fade < 1 {
			v.fade = float32(v.fade + 1.0/64)
			y = float32(float32(y*v.fade) + float32(v.tail*float32(1-v.fade)))
		}
		v.last = y
		left = float32(left + float32(y*i.panL[n]))
		right = float32(right + float32(y*i.panR[n]))
	}
	if i.chorus > 0 {
		mono := float32(.5 * float32(left+right))
		i.chorusLP[0] = float32(i.chorusLP[0] + float32(i.chorusInput*float32(mono-i.chorusLP[0])))
		i.delay[i.write] = i.chorusLP[0]
		var taps [3]float32
		for c := range taps {
			i.chorusPhases[c] = wrap(float32(i.chorusPhases[c] + i.chorusRates[c]))
			d := float32(i.chorusBase + float32(i.chorusDepth*i.sin(i.chorusPhases[c])))
			pos := float32(i.write) - d
			if pos < 0 {
				pos = float32(pos + 8192)
			}
			n := int(pos)
			f := float32(pos - float32(n))
			// Adding the ring length can round a tiny negative position to 8192.
			// Mask both taps after conversion, including that exact boundary.
			taps[c] = float32(i.delay[n&8191] + float32(f*float32(i.delay[(n+1)&8191]-i.delay[n&8191])))
		}
		i.write = (i.write + 1) & 8191
		wetL, wetR := float32(float32(.67*taps[0])+float32(.33*taps[1])), float32(float32(.67*taps[2])+float32(.33*taps[1]))
		// A shared low-pass state plus the BBD input filter limits the clock-like
		// brightness of interpolation without suppressing the stereo movement.
		avg := float32(.5 * float32(wetL+wetR))
		i.chorusLP[1] = float32(i.chorusLP[1] + float32(i.chorusOutput*float32(avg-i.chorusLP[1])))
		correction := float32(i.chorusLP[1] - avg)
		left = float32(float32(left*float32(1-float32(.45*i.chorus))) + float32(i.chorus*float32(wetL+correction)))
		right = float32(float32(right*float32(1-float32(.45*i.chorus))) + float32(i.chorus*float32(wetR+correction)))
	}
	left, right = float32(left*i.gain), float32(right*i.gain)
	// AC coupling after drive, filtering and chorus removes pulse-width bias
	// and offsets introduced by nonlinear or time-varying processing.
	i.dcOutput[0] = float32(float32(left-i.dcInput[0]) + float32(i.dcAlpha*i.dcOutput[0]))
	i.dcOutput[1] = float32(float32(right-i.dcInput[1]) + float32(i.dcAlpha*i.dcOutput[1]))
	i.dcInput[0], i.dcInput[1] = left, right
	return i.dcOutput[0], i.dcOutput[1]
}
