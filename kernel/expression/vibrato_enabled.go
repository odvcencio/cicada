//go:build !tinygo || modular

package expression

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/detmath"
)

type defaultVibrato struct {
	depth            float32
	phase, increment float64
	delay, remaining int64
}

func (*defaultVibrato) supported() bool { return true }

func (v *defaultVibrato) configure(cfg VibratoConfig, rate float32) {
	v.depth, v.phase = cfg.DepthCents, 0
	v.increment = float64(cfg.RateHz) / float64(rate)
	v.delay = int64(math.Round(float64(cfg.DelayMS) * float64(rate) / 1000))
	v.remaining = v.delay
}
func (v *defaultVibrato) reset()       { v.phase, v.remaining = 0, v.delay }
func (v *defaultVibrato) active() bool { return v.depth != 0 && v.increment != 0 }
func (v *defaultVibrato) next() float64 {
	if !v.active() {
		return 0
	}
	if v.remaining > 0 {
		v.remaining--
		return 0
	}
	cents := float64(v.depth) * detmath.Sin(2*math.Pi*v.phase)
	v.phase += v.increment
	if v.phase >= 1 {
		v.phase -= 1
	}
	return cents
}
