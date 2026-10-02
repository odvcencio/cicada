package expressive

import (
	"m31labs.dev/cicada/kernel/dsp/halfband"
	"math"
)

// Guitar is a plucked traveling-wave loop with pick-position excitation,
// pickup comb, frequency-dependent loss, continuous bend and palm damping.
type Guitar struct {
	sr, hz, target, velocity, phase float64
	e, control                      Expression
	loop                            delay
	loss1, loss2, dcIn, dcOut       float64
	released                        bool
	rng                             uint32
	amp                             Amp
}

func NewGuitar(sampleRate int) *Guitar {
	if sampleRate < 8000 || sampleRate > 192000 {
		sampleRate = 48000
	}
	g := &Guitar{sr: float64(sampleRate), hz: 110, target: 110, rng: 1}
	g.loop.init(sampleRate/20 + 8)
	g.amp = NewAmp(sampleRate)
	g.e = Expression{PitchHz: 110, Position: .22, Brightness: .7}
	g.control = g.e
	return g
}
func (g *Guitar) NoteOn(hz, velocity float64) {
	g.hz = clamp(hz, 40, 2000)
	g.target = g.hz
	g.velocity = clamp(velocity, 0, 1)
	g.released = false
	g.e.PitchHz = g.hz
	g.loss1 = 0
	g.loss2 = 0
	g.dcIn = 0
	g.dcOut = 0
	g.phase = 0
	g.amp.Reset()
	for i := range g.loop.data {
		g.loop.data[i] = 0
	}
	n := int(g.sr / g.hz)
	p := clamp(g.e.Position, .05, .45) // triangular displacement: pluck position controls harmonic nulls
	for i := 0; i < n; i++ {
		x := float64(i) / float64(n)
		v := x / p
		if x > p {
			v = (1 - x) / (1 - p)
		}
		g.rng ^= g.rng << 13
		g.rng ^= g.rng >> 17
		g.rng ^= g.rng << 5
		noise := float64(int32(g.rng)) / 2147483648
		g.loop.push(g.velocity * (.65*(2*v-1) + .06*noise))
	}
}
func (g *Guitar) NoteOff() { g.released = true }
func (g *Guitar) SetExpression(e Expression) {
	if finite(e.PitchHz) && e.PitchHz > 0 {
		g.target = clamp(e.PitchHz, 40, 2000)
	}
	e.Position = clamp(e.Position, .05, .45)
	e.Brightness = clamp(e.Brightness, 0, 1)
	e.Vibrato = clamp(e.Vibrato, 0, 100)
	e.Damping = clamp(e.Damping, 0, 1)
	e.Drive = clamp(e.Drive, 0, 1)
	g.e = e
}
func (g *Guitar) Next() float64 {
	alpha := 1 - math.Exp(-1/(.008*g.sr))
	g.control.Position += alpha * (g.e.Position - g.control.Position)
	g.control.Brightness += alpha * (g.e.Brightness - g.control.Brightness)
	g.control.Vibrato += alpha * (g.e.Vibrato - g.control.Vibrato)
	g.control.Damping += alpha * (g.e.Damping - g.control.Damping)
	g.control.Drive += alpha * (g.e.Drive - g.control.Drive)
	g.hz += (g.target - g.hz) * (1 - math.Exp(-1/(.008*g.sr)))
	g.phase += 5 / g.sr
	if g.phase >= 1 {
		g.phase -= 1
	}
	f := g.hz * math.Exp2(g.control.Vibrato*math.Sin(2*math.Pi*g.phase)/1200)
	period := g.sr / f
	x := g.loop.read(period - 1)
	t60 := 4.5 / (1 + 35*g.control.Damping)
	if g.released {
		t60 = .12
	}
	gain := math.Exp(-6.907755 / (f * t60))
	a := gain * (1 - g.control.Brightness) / 4
	b := gain * (1 + g.control.Brightness) / 2
	feedback := a*x + b*g.loss1 + a*g.loss2
	g.loss2 = g.loss1
	g.loss1 = x
	g.loop.push(feedback)
	pickup := x - .72*g.loop.read(period*clamp(g.control.Position, .05, .45))
	hp := pickup - g.dcIn + .995*g.dcOut
	g.dcIn = pickup
	g.dcOut = hp
	return g.amp.Next(hp*.7, g.control.Drive)
}

// Amp is a generic two-stage soft clipper with 4x FIR oversampling, first-order
// antiderivative antialiasing and analytic speaker lowpass. It is not a measured
// amplifier or cabinet model. No samples or impulse responses are used.
type Amp struct {
	up1, up2, down1, down2  halfband.FIR
	previous                [2]float64
	low, dcIn, dcOut, alpha float64
}

func NewAmp(sr int) Amp {
	return Amp{up1: halfband.New(), up2: halfband.New(), down1: halfband.New(), down2: halfband.New(), alpha: 1 - math.Exp(-2*math.Pi*5500/float64(sr))}
}
func (a *Amp) Reset() {
	a.up1.Reset()
	a.up2.Reset()
	a.down1.Reset()
	a.down2.Reset()
	a.previous = [2]float64{}
	a.low = 0
	a.dcIn = 0
	a.dcOut = 0
}
func logcosh(x float64) float64 { v := math.Abs(x); return v + math.Log1p(math.Exp(-2*v)) - math.Ln2 }
func (a *Amp) clip(x float64, stage int) float64 {
	p := a.previous[stage]
	a.previous[stage] = x
	if math.Abs(x-p) < 1e-6 {
		return math.Tanh((x + p) * .5)
	}
	return (logcosh(x) - logcosh(p)) / (x - p)
}
func (a *Amp) Next(x, drive float64) float64 {
	drive = clamp(drive, 0, 1)
	u, v := a.up1.Upsample(x)
	hi := [2]float64{u, v}
	var out [2]float64
	for i, h := range hi {
		p, q := a.up2.Upsample(h)
		p = a.clip(a.clip(p*(1+18*drive), 0)*(1+3*drive), 1)
		q = a.clip(a.clip(q*(1+18*drive), 0)*(1+3*drive), 1)
		out[i] = a.down2.Downsample(p, q)
	}
	y := a.down1.Downsample(out[0], out[1])
	hp := y - a.dcIn + .995*a.dcOut
	a.dcIn = y
	a.dcOut = hp
	a.low += a.alpha * (hp - a.low)
	return .7 * a.low
}
