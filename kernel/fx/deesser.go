package fx

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/detmath"
	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/dsp/subnormal"
)

// DeesserParams controls a stereo-linked split-band de-esser. Only the high
// band is attenuated; the complementary low band keeps body and stereo image.
type DeesserParams struct {
	FrequencyHz, ThresholdDB, Ratio, RangeDB, AttackMs, ReleaseMs, Mix float64
}

func DefaultDeesserParams() DeesserParams { return DeesserParams{6000, -24, 4, 9, 1, 80, 1} }
func (p DeesserParams) Validate() error {
	if !finite(p.FrequencyHz) || !finite(p.ThresholdDB) || !finite(p.Ratio) || !finite(p.RangeDB) || !finite(p.AttackMs) || !finite(p.ReleaseMs) || !finite(p.Mix) || p.FrequencyHz < 2000 || p.FrequencyHz > 12000 || p.ThresholdDB < -60 || p.ThresholdDB > 0 || p.Ratio < 1 || p.Ratio > 20 || p.RangeDB < 0 || p.RangeDB > 24 || p.AttackMs < .1 || p.AttackMs > 20 || p.ReleaseMs < 10 || p.ReleaseMs > 500 || p.Mix < 0 || p.Mix > 1 {
		return validationError("de-esser parameter is invalid or out of range")
	}
	return nil
}

type Deesser struct {
	params                                           DeesserParams
	b0, b1, b2, a1, a2                               float64
	z1, z2                                           [2]float64
	attack, release, detectorAttack, detectorRelease float64
	envelope, reduction                              float64
	fault                                            bool
	frames                                           uint32 // Process calls since New or Reset, for the sweep
}

func NewDeesser(rate int, p DeesserParams) (*Deesser, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 {
		return nil, validationError("unsupported de-esser sample rate")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	// A first-order complementary crossover sums exactly to the original
	// signal. Attenuating its high band gives a smooth shelf, without the
	// cancellation/ripple of subtracting an unmatched second-order highpass.
	k := detmath.Tan(math.Pi * p.FrequencyHz / float64(rate))
	b := 1 / (1 + k)
	d := &Deesser{params: p, b0: b, b1: -b, a1: (k - 1) / (k + 1),
		attack: 1 - detmath.Exp(-1/(p.AttackMs*.001*float64(rate))), release: 1 - detmath.Exp(-1/(p.ReleaseMs*.001*float64(rate))),
		detectorAttack: 1 - detmath.Exp(-1/(.0002*float64(rate))), detectorRelease: 1 - detmath.Exp(-1/(.010*float64(rate)))}
	return d, nil
}
func (d *Deesser) Reset() {
	d.z1, d.z2 = [2]float64{}, [2]float64{}
	d.envelope, d.reduction, d.fault = 0, 0, false
	d.frames = 0
}
func (d *Deesser) Fault() bool              { return d.fault }
func (d *Deesser) LatencyFrames() int       { return 0 }
func (d *Deesser) GainReductionDB() float64 { return d.reduction }

// Silent is an exact-state predicate, including the detector release. It is
// never inferred from output amplitude or from the split-band crossover alone.
func (d *Deesser) Silent() bool {
	return !d.fault && d.z1 == [2]float64{} && d.z2 == [2]float64{} && d.envelope == 0 && d.reduction == 0
}
func (d *Deesser) Process(l, r float32) (float32, float32) {
	if d.fault {
		return 0, 0
	}
	if !finite(float64(l)) || !finite(float64(r)) {
		d.fault = true
		return 0, 0
	}
	if d.params.Mix == 0 || d.params.RangeDB == 0 || d.params.Ratio == 1 {
		return l, r
	}
	if l == 0 && r == 0 && d.Silent() {
		return 0, 0
	}
	in := [2]float64{float64(l), float64(r)}
	var hi [2]float64
	for ch := range in {
		hi[ch] = float64(d.b0*in[ch]) + d.z1[ch]
		d.z1[ch] = float64(d.b1*in[ch]) - float64(d.a1*hi[ch]) + d.z2[ch]
		d.z2[ch] = float64(d.b2*in[ch]) - float64(d.a2*hi[ch])
		if math.Abs(d.z1[ch]) < 1e-30 {
			d.z1[ch] = 0
		}
		if math.Abs(d.z2[ch]) < 1e-30 {
			d.z2[ch] = 0
		}
	}
	instant := math.Max(math.Abs(hi[0]), math.Abs(hi[1]))
	detector := d.detectorRelease
	if instant > d.envelope {
		detector = d.detectorAttack
	}
	d.envelope += float64((instant - d.envelope) * detector)
	target := float64(0)
	if d.envelope > 0 {
		over := float64((20/(math.Log2E*math.Ln10))*fastmath.Log2(d.envelope)) - d.params.ThresholdDB
		target = math.Min(d.params.RangeDB, math.Max(0, over)*(1-1/d.params.Ratio))
	}
	coeff := d.release
	if target > d.reduction {
		coeff = d.attack
	}
	d.reduction += float64((target - d.reduction) * coeff)
	// The detector envelope releases toward zero and, in float64, stalls at
	// a subnormal number seconds into silence, which slows every frame. It
	// reaches the output only through the target above, which is 0 for a
	// subnormal envelope and for 0. The reduction stalls the same way but is
	// not flushed: it feeds 1 - fastmath.Exp2(-reduction*k), which is exactly
	// 0 for a reduction of 0 and -7.5e-8 for a subnormal one, so a flush
	// would change the output. That fix belongs in the consumer.
	d.frames++
	if d.frames%subnormal.SweepFrames == 0 {
		d.envelope = subnormal.Flush64(d.envelope)
	}
	// Keep crossover and detector tails above advancing, even when the
	// crossover is exactly zero. Attenuation cannot affect this sample then.
	if in[0] == 0 && in[1] == 0 && hi[0] == 0 && hi[1] == 0 {
		return 0, 0
	}
	attenuation := (1 - fastmath.Exp2(-d.reduction*(math.Log2E*math.Ln10/20))) * d.params.Mix
	outL, outR := in[0]-float64(hi[0]*attenuation), in[1]-float64(hi[1]*attenuation)
	if !finite(outL) || !finite(outR) || math.Abs(outL) > math.MaxFloat32 || math.Abs(outR) > math.MaxFloat32 {
		d.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}
