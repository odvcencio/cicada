// Package keyboard prepares the optional modeled keyboard family. The core
// WASM kernel does not import this package or any of the keyboard DSP engines.
package keyboard

import (
	"fmt"
	"m31labs.dev/cicada/kernel/voice/clav"
	"m31labs.dev/cicada/kernel/voice/ep"
	"m31labs.dev/cicada/kernel/voice/fm"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/organ"
	"m31labs.dev/cicada/kernel/voice/polykeys"
	"m31labs.dev/cicada/kernel/voice/strings"
	"math"
)

var Names = keyboard.Names

type Parameter = keyboard.Parameter

func init()                              { keyboard.Prepare = New }
func ID(name string) uint8               { return keyboard.ID(name) }
func Name(id uint8) string               { return keyboard.Name(id) }
func Parameters(name string) []Parameter { return keyboard.Parameters(name) }

func DefaultSpec(name string) (keyboard.Spec, error) {
	id := ID(name)
	s := keyboard.Spec{Patch: id}
	s.Controls[127] = 8
	c := &s.Controls
	if id == 0 {
		return s, fmt.Errorf("unknown keys patch %s", name)
	}
	switch {
	case id <= 6:
		p, _ := ep.Patch(name)
		v := []float64{p.PickupPosition, p.PickupDistance, p.HammerFelt, p.Decay, p.Release, p.Drive, p.Tremolo, p.TremoloRate, p.Pan, p.Gain, float64(p.Oversample)}
		for j, x := range v {
			c[j+1] = float32(x)
		}
	case id <= 9:
		p, _ := clav.Patch(name)
		v := []float64{float64(p.Pickup), p.Mute, p.Tangent, p.Click, p.Drive, p.Gain}
		for j, x := range v {
			c[j+1] = float32(x)
		}
	case id <= 13:
		p, _ := organ.Patch(name)
		for j, x := range p.Drawbars {
			c[j+1] = float32(x)
		}
		c[10] = float32(p.Percussion)
		c[11] = boolean(p.PercussionFast)
		c[12] = boolean(p.PercussionSoft)
		c[13] = float32(p.Scanner)
		c[14] = p.KeyClick
		c[15] = p.Leakage
		c[16] = p.Crosstalk
		c[17] = p.Drive
		c[18] = p.RotaryMix
		c[19] = p.MicSpread
		c[20] = boolean(p.RotaryFast)
		c[21] = p.Gain
	case id <= 16:
		p, _ := fm.Patch(name)
		c[1] = p.Gain
		c[2] = p.StereoSpread
		for j, o := range p.Operators {
			v := [12]float32{o.Ratio, o.Detune, o.Level, o.Velocity, o.KeyTracking, o.Pan, o.Feedback, o.Envelope.Attack, o.Envelope.Decay, o.Envelope.Sustain, o.Envelope.Release, p.Output[j]}
			for k, x := range v {
				c[3+j*12+k] = x
			}
		}
		index := 75
		for d := 0; d < 5; d++ {
			for src := d + 1; src < 6; src++ {
				c[index] = p.Routing[d][src]
				index++
			}
		}
	case id <= 20:
		p, _ := polykeys.Patch(name)
		v := []float64{p.Saw, p.Pulse, p.PulseWidth, p.PWM, p.PWMRate, p.Sub, p.Detune, p.Drift, p.Sync, p.Cutoff, p.Resonance, p.KeyTrack, p.FilterEnv, p.Drive, p.Attack, p.Decay, p.Sustain, p.Release, p.Velocity, p.Chorus, p.LevelDB, float64(p.Filter)}
		for j, x := range v {
			c[j+1] = float32(x)
		}
	default:
		p, _ := strings.Patch(name)
		v := []float64{p.Octave16, p.Octave8, p.Octave4, p.Attack, p.Release, p.Cutoff, p.Velocity, p.Ensemble, p.EnsembleRate, p.LevelDB}
		for j, x := range v {
			c[j+1] = float32(x)
		}
	}
	return s, nil
}
func boolean(v bool) float32 {
	if v {
		return 1
	}
	return 0
}
func Validate(s *keyboard.Spec) error {
	if s == nil || Name(s.Patch) == "" {
		return fmt.Errorf("unknown keyboard patch")
	}
	var used [128]bool
	for _, p := range Parameters(Name(s.Patch)) {
		v := s.Controls[p.Index]
		used[p.Index] = true
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < p.Min || v > p.Max || p.Integer && v != float32(int(v)) {
			return fmt.Errorf("keys parameter %s outside range", p.Name)
		}
	}
	for j, v := range s.Controls {
		if !used[j] && v != 0 {
			return fmt.Errorf("unused keyboard control %d must be zero", j)
		}
	}
	if s.Patch <= 6 && s.Controls[11] != 2 && s.Controls[11] != 4 {
		return fmt.Errorf("keys oversample must be 2 or 4")
	}
	switch {
	case s.Patch >= 14 && s.Patch <= 16:
		var level float32
		for op := 0; op < 6; op++ {
			level += s.Controls[5+op*12] * s.Controls[14+op*12]
		}
		if level == 0 {
			return fmt.Errorf("FM keys need an audible carrier")
		}
	case s.Patch >= 17 && s.Patch <= 20:
		if s.Controls[1]+s.Controls[2] == 0 {
			return fmt.Errorf("poly keys need a saw or pulse oscillator")
		}
	case s.Patch == 21:
		if s.Controls[1]+s.Controls[2]+s.Controls[3] == 0 {
			return fmt.Errorf("string machine needs an octave rank")
		}
	}
	return nil
}
func New(rate int, s *keyboard.Spec) (keyboard.Voice, error) {
	if err := Validate(s); err != nil {
		return nil, err
	}
	c := s.Controls
	f := func(n int) float64 { return math.Round(float64(c[n])*1e6) / 1e6 }
	var voice keyboard.Voice
	var err error
	switch {
	case s.Patch <= 6:
		model := ep.Tine
		if s.Patch >= 5 {
			model = ep.Reed
		}
		voice, err = ep.New(rate, ep.Params{Model: model, PickupPosition: f(1), PickupDistance: f(2), HammerFelt: f(3), Decay: f(4), Release: f(5), Drive: f(6), Tremolo: f(7), TremoloRate: f(8), Pan: f(9), Gain: f(10), Oversample: int(c[11])})
	case s.Patch <= 9:
		voice, err = clav.New(rate, clav.Params{Pickup: clav.Pickup(c[1]), Mute: f(2), Tangent: f(3), Click: f(4), Drive: f(5), Gain: f(6)})
	case s.Patch <= 13:
		p := organ.Params{Percussion: organ.Percussion(c[10]), PercussionFast: c[11] == 1, PercussionSoft: c[12] == 1, Scanner: organ.Scanner(c[13]), KeyClick: c[14], Leakage: c[15], Crosstalk: c[16], Drive: c[17], RotaryMix: c[18], MicSpread: c[19], RotaryFast: c[20] == 1, Gain: c[21]}
		for j := range p.Drawbars {
			p.Drawbars[j] = uint8(c[j+1])
		}
		voice, err = organ.New(rate, p)
	case s.Patch <= 16:
		p := fm.Params{Gain: c[1], StereoSpread: c[2]}
		for j := range p.Operators {
			n := 3 + j*12
			p.Operators[j] = fm.Operator{Ratio: c[n], Detune: c[n+1], Level: c[n+2], Velocity: c[n+3], KeyTracking: c[n+4], Pan: c[n+5], Feedback: c[n+6], Envelope: fm.Envelope{Attack: c[n+7], Decay: c[n+8], Sustain: c[n+9], Release: c[n+10]}}
			p.Output[j] = c[n+11]
		}
		index := 75
		for d := 0; d < 5; d++ {
			for src := d + 1; src < 6; src++ {
				p.Routing[d][src] = c[index]
				index++
			}
		}
		voice, err = fm.New(rate, p)
	case s.Patch <= 20:
		voice, err = polykeys.New(rate, polykeys.Params{Saw: f(1), Pulse: f(2), PulseWidth: f(3), PWM: f(4), PWMRate: f(5), Sub: f(6), Detune: f(7), Drift: f(8), Sync: f(9), Cutoff: f(10), Resonance: f(11), KeyTrack: f(12), FilterEnv: f(13), Drive: f(14), Attack: f(15), Decay: f(16), Sustain: f(17), Release: f(18), Velocity: f(19), Chorus: f(20), LevelDB: f(21), Filter: polykeys.Filter(c[22])})
	default:
		voice, err = strings.New(rate, strings.Params{Octave16: f(1), Octave8: f(2), Octave4: f(3), Attack: f(4), Release: f(5), Cutoff: f(6), Velocity: f(7), Ensemble: f(8), EnsembleRate: f(9), LevelDB: f(10)})
	}
	if err != nil {
		return nil, err
	}
	if limiter, ok := voice.(interface{ SetVoiceLimit(int) error }); ok {
		if err = limiter.SetVoiceLimit(int(c[127])); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("keyboard does not support bounded polyphony")
	}
	if err = voice.SetSustain(c[0]); err != nil {
		return nil, err
	}
	return voice, nil
}
