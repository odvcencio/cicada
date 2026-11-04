package expression

import "math"

// VibratoConfig is an instrument's baseline pitch movement. Per-note vibrato
// remains independent and adds to it.
type VibratoConfig struct {
	DepthCents float32
	RateHz     float32
	DelayMS    float32
}

func (v VibratoConfig) Validate() error {
	values := [...]float32{v.DepthCents, v.RateHz, v.DelayMS}
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return validationError("instrument vibrato must be finite")
		}
	}
	if v.DepthCents < 0 || v.DepthCents > 9600 || v.RateHz < 0 || v.RateHz > 100 || v.DelayMS < 0 || v.DelayMS > 10000 {
		return validationError("instrument vibrato is out of range")
	}
	return nil
}
