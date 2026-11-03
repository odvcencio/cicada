package mix

import (
	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/modulate"
)

// DBToLinear is 10^(dB/20) from the fixed Exp2 approximation (relative error
// below 2e-7 for gains from -60 to +6 dB). It makes no math-library call, so a
// modulator can rewrite a track's gain every control-grid point.
func DBToLinear(gainDB float32) float32 {
	return float32(fastmath.Exp2(float64(float64(gainDB) * 0.16609640474436813)))
}

// DBToLinearClamped applies the same conversion for a finite gain already
// clamped to the mix-gain parameter's [-60,6] dB range.
func DBToLinearClamped(gainDB float32) float32 {
	return float32(fastmath.Exp2Finite(float64(float64(gainDB) * 0.16609640474436813)))
}

// NewTrackTable is NewTrack for a track whose gain or pan a modulator moves:
// the same constant-power law, built from DBToLinear and the quarter-wave sine
// table instead of math.Pow, math.Cos and math.Sin. It matches NewTrack within
// 1e-6 relative error but is not bit-identical to it (see TestNewTrackTable).
func NewTrackTable(gainDB, pan float32) Track {
	return NewTrackTableLinear(DBToLinear(gainDB), pan)
}

// NewTrackTableLinear applies the same pan law to a precomputed linear gain.
func NewTrackTableLinear(gain, pan float32) Track {
	// angle = (pan+1)*pi/4 is (pan+1)/8 of a cycle.
	phase := uint32(float64(float64(pan)+1) * (1 << 29))
	const q30 = 1.0 / (1 << 30)
	sin, cos := modulate.SinCosQuarter(phase)
	left := float32(cos) * q30
	right := float32(sin) * q30
	return Track{Left: float32(gain * left), Right: float32(gain * right)}
}
