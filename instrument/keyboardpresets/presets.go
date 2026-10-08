// Package keyboardpresets holds the default control tables for the modeled
// keyboard patches. It is pure so notation can read preset parameters without
// depending on host packages.
package keyboardpresets

import (
	"fmt"

	"m31labs.dev/cicada/kernel/voice/clav"
	"m31labs.dev/cicada/kernel/voice/ep"
	"m31labs.dev/cicada/kernel/voice/fm"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/organ"
	"m31labs.dev/cicada/kernel/voice/polykeys"
	"m31labs.dev/cicada/kernel/voice/strings"
)

// DefaultSpec returns the default controls for one keyboard patch name.
func DefaultSpec(name string) (keyboard.Spec, error) {
	id := keyboard.ID(name)
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
