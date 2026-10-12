// Package keyboard prepares the optional modeled keyboard family. The core
// WASM kernel does not import this package or any of the keyboard DSP engines.
package keyboard

import (
	"fmt"
	"m31labs.dev/cicada/instrument/keyboardpresets"
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

func init()                              { keyboard.Prepare = NewCalibrated }
func ID(name string) uint8               { return keyboard.ID(name) }
func Name(id uint8) string               { return keyboard.Name(id) }
func Parameters(name string) []Parameter { return keyboard.Parameters(name) }

// DefaultSpec returns the default controls for one keyboard patch name.
func DefaultSpec(name string) (keyboard.Spec, error) { return keyboardpresets.DefaultSpec(name) }

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

// LevelTrimDB is the fixed calibration trim for each modeled patch, indexed by
// patch ID minus one (the order of Names). It is applied once, at the voice
// output, by NewCalibrated. It is separate from the user controls gain,
// output, level and velocity, which keep their meaning. The values come from
// measuring each examples/keys reference so that all 21 patches read
// -24 LUFS integrated at 0 dB.
var LevelTrimDB = [len(keyboard.Names)]float32{
	1.95,  // tine_ep
	2.96,  // tine_bell
	5.14,  // tine_bark
	3.84,  // tine_tremolo
	5.01,  // reed_ep
	7.75,  // reed_tremolo
	-5.68, // clav
	-0.55, // clav_muted
	-5.62, // clav_hollow
	4.04,  // tonewheel_organ
	3.80,  // organ_jazz
	4.80,  // organ_full
	5.72,  // organ_soft
	1.32,  // fm_ep
	2.75,  // bell_keys
	4.66,  // fm_bass
	14.51, // brass_stab
	9.69,  // soft_pad
	15.05, // poly_keys
	2.82,  // sync_lead
	2.50,  // string_machine
}

// LevelTrimLinear returns the linear factor for a patch ID, or 1 for an
// unknown ID.
func LevelTrimLinear(patch uint8) float32 {
	if patch < 1 || int(patch) > len(LevelTrimDB) {
		return 1
	}
	return float32(math.Pow(10, float64(LevelTrimDB[patch-1])/20))
}

// calibrated scales the output of one voice by a fixed factor. Every other
// method passes through to the wrapped voice.
type calibrated struct {
	keyboard.Voice
	gain float32
}

func (c *calibrated) NextStereo() (float32, float32) {
	l, r := c.Voice.NextStereo()
	return l * c.gain, r * c.gain
}

// NewCalibrated builds the patch with New and applies its level trim. Native
// playback, offline render and the keys WASM module all prepare voices here.
// New stays untrimmed for tools that need the raw voice, such as the sample
// pack builder.
func NewCalibrated(rate int, s *keyboard.Spec) (keyboard.Voice, error) {
	v, err := New(rate, s)
	if err != nil {
		return nil, err
	}
	gain := LevelTrimLinear(s.Patch)
	if gain == 1 {
		return v, nil
	}
	return &calibrated{Voice: v, gain: gain}, nil
}
