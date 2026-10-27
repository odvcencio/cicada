// Package ampcab provides an original, stereo guitar-style saturation and cabinet.
// Prepare processors before entering the audio callback; each stream needs its own state.
package ampcab

import (
	"errors"
	"math"

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
}

func New(rate int, params Params) (*Processor, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 {
		return nil, errors.New("unsupported amp sample rate")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	p := &Processor{rate: rate, smooth: 1 - math.Exp(-1/(.02*float64(rate))), dcAlpha: math.Exp(-2 * math.Pi * 20 / float64(rate))}
	for i := range p.ir { // Original causal damped speaker responses, sampled at the target rate.
		freq := []float64{2600, 1800, 3600}[i]
		decay := []float64{.00065, .00085, .00048}[i]
		var sum float64
		for j := 0; j < taps; j++ {
			t := float64(j) / float64(rate)
			p.ir[i][j] = math.Exp(-t/decay) * (1 + .25*math.Cos(2*math.Pi*freq*t))
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
	p.targetTone = 1 - math.Exp(-2*math.Pi*(1200+6800*v.Tone)/float64(p.rate))
	p.targetOut = math.Pow(10, v.Output/20)
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
}
func (p *Processor) Process(l, r float32) (float32, float32) {
	p.drive += p.smooth * (p.params.Drive - p.drive)
	p.tone += p.smooth * (p.targetTone - p.tone)
	p.out += p.smooth * (p.targetOut - p.out)
	for i := range p.weights {
		target := 0.0
		if i == p.params.Cabinet {
			target = 1
		}
		p.weights[i] += p.smooth * (target - p.weights[i])
	}
	target := 0.
	if p.params.Bypass {
		target = 1
	}
	p.bypass += p.smooth * (target - p.bypass)
	return float32(p.process(&p.left, float64(l))), float32(p.process(&p.right, float64(r)))
}
func (p *Processor) process(c *channel, x float64) float64 {
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
	gain := 1 + 12*p.drive
	norm := 1 / math.Tanh(gain)
	a0 = math.Tanh(a0*gain) * norm
	a1 = math.Tanh(a1*gain) * norm
	b0 = math.Tanh(b0*gain) * norm
	b1 = math.Tanh(b1*gain) * norm
	a0, a1, b0, b1, c.phase = c.phase, a0, a1, b0, b1
	a = c.down2.Downsample(a0, a1)
	b = c.down2.Downsample(b0, b1)
	y := c.down1.Downsample(a, b)
	c.history[c.pos] = y
	var z float64
	idx := c.pos
	for i := range p.ir[0] {
		h := p.weights[0]*p.ir[0][i] + p.weights[1]*p.ir[1][i] + p.weights[2]*p.ir[2][i]
		z += h * c.history[idx]
		idx--
		if idx < 0 {
			idx = taps - 1
		}
	}
	c.pos = (c.pos + 1) % taps
	c.tone += p.tone * (z - c.tone)
	return (1-p.bypass)*c.tone*p.out + p.bypass*dry
}
