// Package ampcab provides an original, stereo guitar-style saturation and cabinet.
// Prepare processors before entering the audio callback; each stream needs its own state.
package ampcab

import (
	"errors"
	"math"

	"m31labs.dev/cicada/kernel/dsp/detmath"
	"m31labs.dev/cicada/kernel/dsp/subnormal"

	"m31labs.dev/cicada/kernel/dsp/halfband"
)

// Latency is the exact linear-stage group delay: the cascaded FIR delay plus
// one 4x-rate sample of phase alignment.
const Latency = 31
const taps = 96

// Drive and Tone are unitless 0..1, Output is dB (-24..12), and Cabinet is
// 0 (open), 1 (warm), or 2 (bright). Bypass retains the processing latency.
type Params struct {
	Drive, Tone, Output float64
	Cabinet             int
	Bypass              bool
}

func DefaultParams() Params { return Params{Drive: .35, Tone: .5, Output: 0, Cabinet: 0} }
func (p Params) Validate() error {
	for _, v := range []float64{p.Drive, p.Tone, p.Output} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("non-finite amp control")
		}
	}
	if p.Drive < 0 || p.Drive > 1 || p.Tone < 0 || p.Tone > 1 || p.Output < -24 || p.Output > 12 || p.Cabinet < 0 || p.Cabinet > 2 {
		return errors.New("amp control out of range")
	}
	return nil
}

type channel struct {
	up1, up2, down2, down1 halfband.FIR
	history                [taps]float64
	pos                    int
	tone                   float64
	phase                  float64
	dc, previousInput      float64
	dry                    [Latency]float64
	dryPos                 int
}
type Processor struct {
	rate                             int
	params                           Params
	left, right                      channel
	ir                               [3][taps]float64
	smooth, drive, tone, out, bypass float64
	targetTone, targetOut, dcAlpha   float64
	weights                          [3]float64
	// normGain and norm cache 1/tanh(gain) for the drive's current gain: both
	// channels use it every frame, and it changes only while the drive moves.
	normGain, norm float64
	frames         uint32 // Process calls since New or Reset, for flushSubnormals
	// blend caches the three impulses mixed by weights, keyed on the weights'
	// bits: both channels use it every frame, and it changes only while a
	// cabinet change fades in. The values are the ones the mix computed per
	// channel before, so the output bits are the same.
	blend        [taps]float64
	blendWeights [3]uint64
	blendValid   bool
}

func New(rate int, params Params) (*Processor, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 {
		return nil, errors.New("unsupported amp sample rate")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	p := &Processor{rate: rate, smooth: 1 - detmath.Exp(-1/(.02*float64(rate))), dcAlpha: detmath.Exp(-2 * math.Pi * 20 / float64(rate))}
	for i := range p.ir { // Original causal damped speaker responses, sampled at the target rate.
		freq := []float64{2600, 1800, 3600}[i]
		decay := []float64{.00065, .00085, .00048}[i]
		var sum float64
		for j := 0; j < taps; j++ {
			t := float64(j) / float64(rate)
			p.ir[i][j] = float64(detmath.Exp(-t/decay) * (1 + float64(.25*detmath.Cos(2*math.Pi*freq*t))))
			sum += p.ir[i][j]
		}
		for j := range p.ir[i] {
			p.ir[i][j] /= sum
		}
	}
	for _, c := range []*channel{&p.left, &p.right} {
		halfband.Init(&c.up1)
		halfband.Init(&c.up2)
		halfband.Init(&c.down1)
		halfband.Init(&c.down2)
	}
	p.SetParams(params)
	p.Reset()
	return p, nil
}
func (p *Processor) SetParams(v Params) error {
	if err := v.Validate(); err != nil {
		return err
	}
	p.params = v
	p.targetTone = 1 - detmath.Exp(-2*math.Pi*(1200+float64(6800*v.Tone))/float64(p.rate))
	p.targetOut = detmath.Pow(10, v.Output/20)
	return nil
}

// Params returns the current targets. Controls are owned by the audio thread.
func (p *Processor) Params() Params { return p.params }

// SetParamsImmediate snaps continuous targets without clearing audio history.
func (p *Processor) SetParamsImmediate(v Params) error {
	if err := p.SetParams(v); err != nil {
		return err
	}
	p.drive, p.tone, p.out = v.Drive, p.targetTone, p.targetOut
	return nil
}
func (p *Processor) LatencyFrames() int { return Latency }
func (p *Processor) Reset() {
	p.left.up1.Reset()
	p.left.up2.Reset()
	p.left.down1.Reset()
	p.left.down2.Reset()
	p.right.up1.Reset()
	p.right.up2.Reset()
	p.right.down1.Reset()
	p.right.down2.Reset()
	p.left.history = [taps]float64{}
	p.right.history = [taps]float64{}
	p.left.dry = [Latency]float64{}
	p.right.dry = [Latency]float64{}
	p.left.pos = 0
	p.right.pos = 0
	p.left.dryPos = 0
	p.right.dryPos = 0
	p.left.tone = 0
	p.right.tone = 0
	p.left.phase = 0
	p.right.phase = 0
	p.left.dc, p.left.previousInput = 0, 0
	p.right.dc, p.right.previousInput = 0, 0
	p.drive = p.params.Drive
	p.tone = p.targetTone
	p.out = p.targetOut
	p.weights = [3]float64{}
	p.weights[p.params.Cabinet] = 1
	p.bypass = 0
	if p.params.Bypass {
		p.bypass = 1
	}
	p.frames = 0
}

// flushSubnormals replaces a subnormal value with a zero of the same sign
// (kernel/dsp/subnormal) in the values that glide to exactly zero: the drive,
// the bypass blend, the weight of every cabinet but the selected one (a
// cabinet switch takes the old one to zero, and each weight multiplies every
// impulse-response tap), and each channel's DC blocker and tone filter, which
// decay toward zero in silence. Without it each stalls at a subnormal number
// and slows every frame from then on. Once the DC blocker is zero the FIR
// stages behind it hold exact zeros after their length. Each reaches the
// output only through sums with normal values or the float32 conversion,
// which absorb it.
func (p *Processor) flushSubnormals() {
	p.drive, p.bypass = subnormal.Flush64(p.drive), subnormal.Flush64(p.bypass)
	for i := range p.weights {
		p.weights[i] = subnormal.Flush64(p.weights[i])
	}
	for _, c := range [...]*channel{&p.left, &p.right} {
		c.dc, c.tone = subnormal.Flush64(c.dc), subnormal.Flush64(c.tone)
	}
}
func (p *Processor) Process(l, r float32) (float32, float32) {
	p.drive += float64(p.smooth * (p.params.Drive - p.drive))
	p.tone += float64(p.smooth * (p.targetTone - p.tone))
	p.out += float64(p.smooth * (p.targetOut - p.out))
	for i := range p.weights {
		target := 0.0
		if i == p.params.Cabinet {
			target = 1
		}
		p.weights[i] += float64(p.smooth * (target - p.weights[i]))
	}
	target := 0.
	if p.params.Bypass {
		target = 1
	}
	p.bypass += float64(p.smooth * (target - p.bypass))
	p.frames++
	if p.frames%subnormal.SweepFrames == 0 {
		p.flushSubnormals()
	}
	gain := 1 + float64(12*p.drive)
	if gain != p.normGain {
		p.normGain, p.norm = gain, 1/detmath.Tanh(gain)
	}
	if w := [3]uint64{math.Float64bits(p.weights[0]), math.Float64bits(p.weights[1]), math.Float64bits(p.weights[2])}; !p.blendValid || w != p.blendWeights {
		p.blendWeights, p.blendValid = w, true
		for i := range p.blend {
			p.blend[i] = float64(p.weights[0]*p.ir[0][i]) + float64(p.weights[1]*p.ir[1][i]) + float64(p.weights[2]*p.ir[2][i])
		}
	}
	return float32(p.process(&p.left, float64(l), gain)), float32(p.process(&p.right, float64(r), gain))
}
func (p *Processor) process(c *channel, x, gain float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		x = 0
	}
	dry := c.dry[c.dryPos]
	c.dry[c.dryPos] = x
	c.dryPos = (c.dryPos + 1) % Latency
	filtered := p.dcAlpha * (c.dc + x - c.previousInput)
	c.previousInput, c.dc = x, filtered
	a, b := c.up1.Upsample(filtered)
	a0, a1 := c.up2.Upsample(a)
	b0, b1 := c.up2.Upsample(b)
	norm := p.norm
	// Per-sample saturation: the second tier (detmath package comment); the
	// normaliser above is a coefficient and stays on the exact Tanh.
	a0 = detmath.TanhNear(a0*gain) * norm
	a1 = detmath.TanhNear(a1*gain) * norm
	b0 = detmath.TanhNear(b0*gain) * norm
	b1 = detmath.TanhNear(b1*gain) * norm
	a0, a1, b0, b1, c.phase = c.phase, a0, a1, b0, b1
	a = c.down2.Downsample(a0, a1)
	b = c.down2.Downsample(b0, b1)
	y := c.down1.Downsample(a, b)
	c.history[c.pos] = y
	var z float64
	idx := c.pos
	for _, h := range p.blend {
		z += float64(h * c.history[idx])
		idx--
		if idx < 0 {
			idx = taps - 1
		}
	}
	c.pos = (c.pos + 1) % taps
	c.tone += float64(p.tone * (z - c.tone))
	return float64((1-p.bypass)*c.tone*p.out) + float64(p.bypass*dry)
}
