// Package strings implements divide-down string keys with a triple ensemble.
// Shared integer phase generators preserve octave coherence between all notes.
package strings

import "math"

const MaxVoices = 8

type Error string

func (e Error) Error() string { return string(e) }

// Params times are seconds. Cutoff is Hz and LevelDB is dB; other controls are
// normalized except EnsembleRate, which scales the three asynchronous LFOs.
type Params struct {
	Octave16, Octave8, Octave4                  float64
	Attack, Release, Cutoff, Velocity, Ensemble float64
	EnsembleRate, LevelDB                       float64
}

func DefaultParams() Params {
	return Params{Octave16: .18, Octave8: .7, Octave4: .3, Attack: .12, Release: .85,
		Cutoff: 4700, Velocity: .35, Ensemble: .78, EnsembleRate: 1, LevelDB: -10}
}

func Patch(name string) (Params, error) {
	if name != "string_machine" {
		return Params{}, Error("unknown string machine patch")
	}
	return DefaultParams(), nil
}

func (p Params) Validate() error {
	values := [...]float64{p.Octave16, p.Octave8, p.Octave4, p.Attack, p.Release, p.Cutoff, p.Velocity, p.Ensemble, p.EnsembleRate, p.LevelDB}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Error("string machine parameters must be finite")
		}
	}
	if p.Octave16 < 0 || p.Octave16 > 1 || p.Octave8 < 0 || p.Octave8 > 1 || p.Octave4 < 0 || p.Octave4 > 1 || p.Octave16+p.Octave8+p.Octave4 == 0 {
		return Error("string machine octave level is out of range")
	}
	if p.Attack < .001 || p.Attack > 10 || p.Release < .01 || p.Release > 10 || p.Cutoff < 200 || p.Cutoff > 16000 || p.Velocity < 0 || p.Velocity > 1 || p.Ensemble < 0 || p.Ensemble > 1 || p.EnsembleRate < .1 || p.EnsembleRate > 3 || p.LevelDB < -60 || p.LevelDB > 6 {
		return Error("string machine envelope or ensemble parameter is out of range")
	}
	return nil
}

type voice struct {
	note             uint8
	stage            uint8
	held             bool
	amp, velocity    float32
	last, tail, fade float32
	age              uint64
}

type pitch struct {
	class uint8
	shift uint8
	delta float32
}

// Instrument owns fixed buffers; only New allocates.
type Instrument struct {
	voices                     [8]voice
	phases, increments         [12]uint64
	pitches                    [88]pitch
	sine                       [1025]float32
	delay                      [8192]float32
	slow, fast                 [3]float32
	slowRate, fastRate         [3]float32
	baseDelay, slowDepth       [3]float32
	fastDepth                  float32
	attack, release, velocity  float32
	octave16, octave8, octave4 float32
	lowpass, highpass, gain    float32
	lastInput, hp, lp          float32
	ensemble, bbdAlpha, bbd    float32
	write                      int
	voiceLimit                 int
	clock                      uint64
	sustain                    bool
}

func New(rate int, params Params) (*Instrument, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 && rate != 192000 {
		return nil, Error("string machine sample rate must be 44100, 48000, 96000, or 192000")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	i := &Instrument{voiceLimit: MaxVoices}
	sr := float64(rate)
	for c := range i.increments {
		top := 108 - ((108 - c) % 12)
		freq := 440 * math.Pow(2, (float64(top)-69)/12)
		i.increments[c] = uint64(math.Round(freq / sr * 4294967296))
	}
	for n := range i.pitches {
		note := n + 21
		class := note % 12
		top := 108 - ((108 - class) % 12)
		shift := uint8((top - note) / 12)
		i.pitches[n] = pitch{uint8(class), shift, float32(float64(i.increments[class]) / (4294967296 * float64(uint64(1)<<shift)))}
	}
	for s := range i.sine {
		i.sine[s] = float32(math.Sin(2 * math.Pi * float64(s) / 1024))
	}
	i.attack = float32(1 / (params.Attack * sr))
	i.release = float32(math.Exp(-4.605170186 / (params.Release * sr)))
	i.velocity = float32(params.Velocity)
	norm := 1 / (params.Octave16 + params.Octave8 + params.Octave4)
	i.octave16, i.octave8, i.octave4 = float32(params.Octave16*norm), float32(params.Octave8*norm), float32(params.Octave4*norm)
	i.lowpass = float32(1 - math.Exp(-2*math.Pi*params.Cutoff/sr))
	i.highpass = float32(math.Exp(-2 * math.Pi * 65 / sr))
	i.gain, i.ensemble = float32(.42*math.Pow(10, params.LevelDB/20)), float32(params.Ensemble)
	i.bbdAlpha = float32(1 - math.Exp(-2*math.Pi*6500/sr))
	i.fastDepth = float32(sr * .00065)
	for c := range i.slow {
		i.slow[c], i.fast[c] = float32(c)*.317, float32(c)*.271
		i.slowRate[c] = float32((.59 + float64(c)*.123) * params.EnsembleRate / sr)
		i.fastRate[c] = float32((5.73 + float64(c)*.617) * params.EnsembleRate / sr)
		i.baseDelay[c] = float32((.016 + float64(c)*.0013) * sr)
		i.slowDepth[c] = float32((.003 + float64(c)*.0002) * sr)
	}
	return i, nil
}

func (i *Instrument) NoteOn(note, velocity uint8) error {
	if note < 21 || note > 108 || velocity > 127 {
		return Error("string machine note must be 21..108 and velocity 0..127")
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
	i.voices[slot] = voice{note: note, stage: 1, held: true, age: i.clock,
		velocity: float32(float32(1-i.velocity) + float32(i.velocity*vel)), tail: tail}
	return nil
}

// SetVoiceLimit sets polyphony to 1..8 without allocating. Reducing the limit
// immediately clears voices outside it; Reset preserves the configured limit.
func (i *Instrument) SetVoiceLimit(count int) error {
	if count < 1 || count > MaxVoices {
		return Error("string machine voice limit must be 1..8")
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
			if !i.sustain {
				v.stage = 3
			}
		}
	}
}

func (i *Instrument) AllNotesOff() {
	i.sustain = false
	for n := range i.voices {
		if i.voices[n].stage != 0 {
			i.voices[n].held, i.voices[n].stage = false, 3
		}
	}
}

func (i *Instrument) SetSustain(value float32) error {
	if value != value || value < 0 || value > 1 {
		return Error("string machine sustain pedal must be 0..1")
	}
	i.sustain = value >= .5
	if !i.sustain {
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

// Reset immediately silences notes and ensemble tails and restarts dividers.
func (i *Instrument) Reset() {
	clear(i.voices[:])
	clear(i.phases[:])
	clear(i.delay[:])
	i.lastInput, i.hp, i.lp, i.bbd = 0, 0, 0, 0
	i.write, i.clock, i.sustain = 0, 0, false
	for c := range i.slow {
		i.slow[c], i.fast[c] = float32(c)*.317, float32(c)*.271
	}
}

func wrap(x float32) float32 { return float32(x - float32(int(x))) }

func (i *Instrument) sin(phase float32) float32 {
	x := float32(phase * 1024)
	n := int(x)
	f := float32(x - float32(n))
	return float32(i.sine[n] + float32(f*float32(i.sine[n+1]-i.sine[n])))
}

func saw(phase, delta float32) float32 {
	y := float32(2*phase - 1)
	if phase < delta {
		x := float32(phase / delta)
		y = float32(y - float32(float32(x+x)-float32(x*x)-1))
	} else if phase > float32(1-delta) {
		x := float32(float32(phase-1) / delta)
		y = float32(y - float32(float32(x*x)+float32(x+x)+1))
	}
	return y
}

func (i *Instrument) NextStereo() (float32, float32) {
	var mono float32
	for n := range i.voices {
		v := &i.voices[n]
		if v.stage == 0 {
			continue
		}
		if v.stage == 1 {
			v.amp = float32(v.amp + i.attack)
			if v.amp >= 1 {
				v.amp, v.stage = 1, 2
			}
		} else if v.stage == 3 {
			v.amp = float32(v.amp * i.release)
			if v.amp < 1e-6 {
				v.stage, v.last = 0, 0
				continue
			}
		}
		p := i.pitches[v.note-21]
		// Bits above the 32-bit fractional phase carry the octave divider state.
		phase := float32((i.phases[p.class]>>p.shift)&0xffffffff) * (1.0 / 4294967296)
		phase16 := float32((i.phases[p.class]>>(p.shift+1))&0xffffffff) * (1.0 / 4294967296)
		y := float32(i.octave8 * saw(phase, p.delta))
		y = float32(y + float32(i.octave16*saw(phase16, float32(p.delta*.5))))
		if p.delta < .24 {
			y = float32(y + float32(i.octave4*saw(wrap(float32(phase*2)), float32(p.delta*2))))
		}
		y = float32(y * float32(v.amp*v.velocity))
		if v.fade < 1 {
			v.fade = float32(v.fade + 1.0/64)
			y = float32(float32(y*v.fade) + float32(v.tail*float32(1-v.fade)))
		}
		v.last = y
		mono = float32(mono + y)
	}
	for c := range i.phases {
		i.phases[c] += i.increments[c]
	}
	i.hp = float32(i.highpass * float32(float32(i.hp+mono)-i.lastInput))
	i.lastInput = mono
	i.lp = float32(i.lp + float32(i.lowpass*float32(i.hp-i.lp)))
	mono = i.lp
	left, right := mono, mono
	if i.ensemble > 0 {
		i.bbd = float32(i.bbd + float32(i.bbdAlpha*float32(mono-i.bbd)))
		i.delay[i.write] = i.bbd
		var taps [3]float32
		for c := range taps {
			i.slow[c] = wrap(float32(i.slow[c] + i.slowRate[c]))
			i.fast[c] = wrap(float32(i.fast[c] + i.fastRate[c]))
			d := float32(i.baseDelay[c] + float32(i.slowDepth[c]*i.sin(i.slow[c])))
			d = float32(d + float32(i.fastDepth*i.sin(i.fast[c])))
			pos := float32(i.write) - d
			if pos < 0 {
				pos = float32(pos + 8192)
			}
			n := int(pos)
			f := float32(pos - float32(n))
			// Float32 rounding can produce 8192 at the circular-buffer boundary.
			taps[c] = float32(i.delay[n&8191] + float32(f*float32(i.delay[(n+1)&8191]-i.delay[n&8191])))
		}
		i.write = (i.write + 1) & 8191
		wetL, wetR := float32(float32(.67*taps[0])+float32(.33*taps[1])), float32(float32(.67*taps[2])+float32(.33*taps[1]))
		left = float32(float32(mono*float32(1-float32(.6*i.ensemble))) + float32(i.ensemble*wetL))
		right = float32(float32(mono*float32(1-float32(.6*i.ensemble))) + float32(i.ensemble*wetR))
	}
	return float32(left * i.gain), float32(right * i.gain)
}
