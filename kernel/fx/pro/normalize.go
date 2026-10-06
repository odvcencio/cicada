package pro

import (
	"math"

	"m31labs.dev/cicada/kernel/loudness"
)

// Normalization reports the actual gain and measurements, including targets
// which peak constraints make unreachable. Normalization is a host operation;
// it is deliberately separate from the render callback and live limiter.
type Normalization struct {
	Before        loudness.Result
	After         loudness.Result
	TargetLUFS    float64
	AppliedGainDB float64
	PeakLimited   bool
	Reachable     bool
}

// NormalizeStereo measures a full programme, then applies a single stereo gain
// in place. It preserves crest factor rather than silently limiting to meet a
// loudness target. A 16-phase, 64-tap reconstruction supplements the meter's
// true-peak estimate; 0.1 dB reserve accounts for phase-grid under-reading.
// Targets require at least one valid gated loudness block; silence is unchanged.
func NormalizeStereo(sampleRate int, left, right []float32, targetLUFS, ceilingDBTP float64) (Normalization, error) {
	result := Normalization{TargetLUFS: targetLUFS}
	if !validRate(sampleRate) {
		return result, ErrSampleRate
	}
	if len(left) != len(right) {
		return result, loudness.ErrChannelCount
	}
	if !finite(targetLUFS) || !finite(ceilingDBTP) || targetLUFS < -36 || targetLUFS > -5 || ceilingDBTP < -12 || ceilingDBTP > 0 {
		return result, ErrParams
	}
	meter, err := loudness.New(sampleRate)
	if err != nil {
		return result, err
	}
	if err = meter.ProcessBlock(left, right); err != nil {
		return result, err
	}
	if err = meter.Finish(); err != nil {
		return result, err
	}
	result.Before = meter.Metrics()
	// Integrated loudness needs a complete 400 ms gating block. Keep short
	// impacts unchanged instead of deriving gain from a partial window.
	if result.Before.Frames < uint64(math.Round(.4*float64(sampleRate))) {
		result.Before.IntegratedLUFS = math.Inf(-1)
		result.After = result.Before
		return result, nil
	}
	result.After = result.Before
	if !finite(result.Before.IntegratedLUFS) {
		return result, nil
	}
	requested := targetLUFS - result.Before.IntegratedLUFS
	peak := math.Max(result.Before.TruePeak, reconstructionPeak(left, right))
	result.Before.TruePeak = peak
	result.Before.TruePeakDBTP = 20 * math.Log10(peak)
	allowed := ceilingDBTP - .1 - 20*math.Log10(peak)
	result.AppliedGainDB = math.Min(requested, allowed)
	result.PeakLimited = allowed < requested
	gain := dbGain(result.AppliedGainDB)
	for i := range left {
		left[i] = float32(float64(left[i]) * gain)
		right[i] = float32(float64(right[i]) * gain)
	}
	meter.Reset()
	if err = meter.ProcessBlock(left, right); err != nil {
		return result, err
	}
	if err = meter.Finish(); err != nil {
		return result, err
	}
	result.After = meter.Metrics()
	// A constant gain scales every interpolated peak identically. Include a
	// float32 rounding reserve rather than run the long FIR a second time.
	result.After.TruePeak = math.Max(result.After.TruePeak, peak*gain*(1+1e-7))
	if result.After.TruePeak > 0 {
		result.After.TruePeakDBTP = 20 * math.Log10(result.After.TruePeak)
	}
	result.Reachable = math.Abs(result.After.IntegratedLUFS-targetLUFS) <= .5
	return result, nil
}

// reconstructionPeak includes zero-extended programme boundaries. The detector
// is deliberately longer and denser than the live limiter's interpolation.
func reconstructionPeak(left, right []float32) float64 {
	const taps, half, phases = 64, 32, 16
	var coeff [phases - 1][taps]float64
	for phase := range coeff {
		fraction, sum := float64(phase+1)/phases, 0.0
		for tap := range coeff[phase] {
			x := float64(tap-(half-1)) - fraction
			sinc := math.Sin(math.Pi*x) / (math.Pi * x)
			position := float64(tap) / (taps - 1)
			window := .42 - .5*math.Cos(2*math.Pi*position) + .08*math.Cos(4*math.Pi*position)
			coeff[phase][tap] = sinc * window
			sum += coeff[phase][tap]
		}
		for tap := range coeff[phase] {
			coeff[phase][tap] /= sum
		}
	}
	peak := 0.0
	for center := -half; center < len(left)+half; center++ {
		if center >= 0 && center < len(left) {
			peak = math.Max(peak, math.Max(math.Abs(float64(left[center])), math.Abs(float64(right[center]))))
		}
		for phase := range coeff {
			l, r := 0.0, 0.0
			for tap, h := range coeff[phase] {
				i := center + tap - (half - 1)
				if i >= 0 && i < len(left) {
					l += h * float64(left[i])
					r += h * float64(right[i])
				}
			}
			peak = math.Max(peak, math.Max(math.Abs(l), math.Abs(r)))
		}
	}
	return peak
}
