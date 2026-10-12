package fx

import (
	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"math"
)

// DeesserParams controls a stereo-linked split-band de-esser. Only the high
// band is attenuated; the complementary low band keeps body and stereo image.
type DeesserParams struct {
	FrequencyHz, ThresholdDB, Ratio, RangeDB, AttackMs, ReleaseMs, Mix float64
}

func DefaultDeesserParams() DeesserParams { return DeesserParams{6000, -24, 4, 9, 1, 80, 1} }
func (p DeesserParams) Validate() error {
	if !finite(p.FrequencyHz) || !finite(p.ThresholdDB) || !finite(p.Ratio) || !finite(p.RangeDB) || !finite(p.AttackMs) || !finite(p.ReleaseMs) || !finite(p.Mix) || p.FrequencyHz < 2000 || p.FrequencyHz > 12000 || p.ThresholdDB < -60 || p.ThresholdDB > 0 || p.Ratio < 1 || p.Ratio > 20 || p.RangeDB < 0 || p.RangeDB > 24 || p.AttackMs < .1 || p.AttackMs > 20 || p.ReleaseMs < 10 || p.ReleaseMs > 500 || p.Mix < 0 || p.Mix > 1 {
		return Error("de-esser parameter is invalid or out of range")
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
}

func NewDeesser(rate int, p DeesserParams) (*Deesser, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 {
		return nil, Error("unsupported de-esser sample rate")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	// A first-order complementary crossover sums exactly to the original
	// signal. Attenuating its high band gives a smooth shelf, without the
	// cancellation/ripple of subtracting an unmatched second-order highpass.
	k := math.Tan(math.Pi * p.FrequencyHz / float64(rate))
	b := 1 / (1 + k)
	d := &Deesser{params: p, b0: b, b1: -b, a1: (k - 1) / (k + 1),
		attack: 1 - math.Exp(-1/(p.AttackMs*.001*float64(rate))), release: 1 - math.Exp(-1/(p.ReleaseMs*.001*float64(rate))),
		detectorAttack: 1 - math.Exp(-1/(.0002*float64(rate))), detectorRelease: 1 - math.Exp(-1/(.010*float64(rate)))}
	return d, nil
}
func (d *Deesser) Reset() {
	d.z1, d.z2 = [2]float64{}, [2]float64{}
	d.envelope, d.reduction, d.fault = 0, 0, false
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
		hi[ch] = d.b0*in[ch] + d.z1[ch]
		d.z1[ch] = d.b1*in[ch] - d.a1*hi[ch] + d.z2[ch]
		d.z2[ch] = d.b2*in[ch] - d.a2*hi[ch]
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
	d.envelope += (instant - d.envelope) * detector
	target := float64(0)
	if d.envelope > 0 {
		over := (20/(math.Log2E*math.Ln10))*fastmath.Log2(d.envelope) - d.params.ThresholdDB
		target = math.Min(d.params.RangeDB, math.Max(0, over)*(1-1/d.params.Ratio))
	}
	coeff := d.release
	if target > d.reduction {
		coeff = d.attack
	}
	d.reduction += (target - d.reduction) * coeff
	// Keep crossover and detector tails above advancing, even when the
	// crossover is exactly zero. Attenuation cannot affect this sample then.
	if in[0] == 0 && in[1] == 0 && hi[0] == 0 && hi[1] == 0 {
		return 0, 0
	}
	attenuation := (1 - fastmath.Exp2(-d.reduction*(math.Log2E*math.Ln10/20))) * d.params.Mix
	outL, outR := in[0]-hi[0]*attenuation, in[1]-hi[1]*attenuation
	if !finite(outL) || !finite(outR) || math.Abs(outL) > math.MaxFloat32 || math.Abs(outR) > math.MaxFloat32 {
		d.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}
