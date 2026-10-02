package expressive

import "math"

// Bow is an experimental, monophonic two-rail bowed-string waveguide.
// The velocity-dependent friction junction follows the waveguide bowed-string
// model described by Julius O. Smith, Physical Audio Signal Processing,
// https://ccrma.stanford.edu/~jos/pasp/Bowed_Strings.html . No samples or IRs are used.
// Controls are smoothed; the friction and string run at twice the output rate.
// Bow is single-owner: callers must serialize control changes and rendering.
type Bow struct {
	rate                                                float64
	bridge, neck                                        bowDelay
	target, control                                     Expression
	pitch, velocity, envelope, phase, loss, dcIn, dcOut float64
	gate                                                bool
	body                                                [3]bowResonator
	fir                                                 [31]float64
	history                                             [31]float64
	index                                               int
}

type bowDelay struct {
	data  []float64
	index int
}

func (d *bowDelay) read(n float64) float64 {
	n = bowBound(n, 1, float64(len(d.data)-2))
	p := float64(d.index) - n
	if p < 0 {
		p += float64(len(d.data))
	}
	i := int(p)
	f := p - float64(i)
	j := i + 1
	if j == len(d.data) {
		j = 0
	}
	return d.data[i]*(1-f) + d.data[j]*f
}
func (d *bowDelay) push(x float64) {
	d.data[d.index] = x
	d.index++
	if d.index == len(d.data) {
		d.index = 0
	}
}

type bowResonator struct{ a, r2, gain, y1, y2 float64 }

func (r *bowResonator) tick(x float64) float64 {
	y := r.gain*x + r.a*r.y1 - r.r2*r.y2
	r.y2 = r.y1
	r.y1 = y
	return y
}
func bowBound(x, lo, hi float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return lo
	}
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func NewBow(sampleRate int) *Bow {
	if sampleRate < 8000 || sampleRate > 192000 {
		sampleRate = 48000
	}
	b := &Bow{rate: float64(sampleRate) * 2, pitch: 220}
	b.bridge.data = make([]float64, int(b.rate/30)+8)
	b.neck.data = make([]float64, int(b.rate/30)+8)
	b.target = Expression{PitchHz: 220, Pressure: 0.55, Position: 0.24, Brightness: 0.6, Damping: 0.15}
	b.control = b.target
	// Analytic broad body modes, deliberately not fitted to a recorded violin.
	for i, f := range []float64{280, 530, 2350} {
		r := math.Exp(-math.Pi * []float64{95, 150, 900}[i] / b.rate)
		w := 2 * math.Pi * f / b.rate
		b.body[i] = bowResonator{a: 2 * r * math.Cos(w), r2: r * r, gain: (1 - r) * math.Sin(w)}
	}
	// Blackman-windowed sinc decimator: nonlinear junction runs at 2x.
	sum := 0.0
	for i := range b.fir {
		x := float64(i - 15)
		v := 0.45
		if x != 0 {
			v = math.Sin(0.45*math.Pi*x) / (math.Pi * x)
		}
		v *= 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/30) + 0.08*math.Cos(4*math.Pi*float64(i)/30)
		b.fir[i] = v
		sum += v
	}
	for i := range b.fir {
		b.fir[i] /= sum
	}
	return b
}

func (b *Bow) NoteOn(hz, velocity float64) {
	b.target.PitchHz = bowBound(hz, 40, 2000)
	if !b.gate && b.envelope < 0.001 {
		b.pitch = b.target.PitchHz
	}
	b.velocity = bowBound(velocity, 0, 1)
	b.gate = true
}
func (b *Bow) NoteOff() { b.gate = false }
func (b *Bow) SetExpression(e Expression) {
	if e.PitchHz > 0 && !math.IsInf(e.PitchHz, 0) {
		b.target.PitchHz = bowBound(e.PitchHz, 40, 2000)
	}
	b.target.Pressure = bowBound(e.Pressure, 0, 1)
	b.target.Position = bowBound(e.Position, 0, 1)
	b.target.Brightness = bowBound(e.Brightness, 0, 1)
	b.target.Vibrato = bowBound(e.Vibrato, 0, 100)
	b.target.Damping = bowBound(e.Damping, 0, 1)
}

func (b *Bow) Next() float64 {
	for sub := 0; sub < 2; sub++ {
		smooth := 1 - math.Exp(-1/(0.008*b.rate))
		b.pitch += smooth * (b.target.PitchHz - b.pitch)
		b.control.Pressure += smooth * (b.target.Pressure - b.control.Pressure)
		b.control.Position += smooth * (b.target.Position - b.control.Position)
		b.control.Brightness += smooth * (b.target.Brightness - b.control.Brightness)
		b.control.Vibrato += smooth * (b.target.Vibrato - b.control.Vibrato)
		b.control.Damping += smooth * (b.target.Damping - b.control.Damping)
		goal := 0.0
		tau := 0.035
		if b.gate {
			goal = b.velocity
			tau = 0.018
		}
		b.envelope += (goal - b.envelope) * (1 - math.Exp(-1/(tau*b.rate)))
		b.phase += 5 / b.rate
		if b.phase >= 1 {
			b.phase--
		}
		freq := b.pitch * math.Exp2(b.control.Vibrato*math.Sin(2*math.Pi*b.phase)/1200)
		cutoff := 2200 + 8500*b.control.Brightness
		pole := math.Exp(-2 * math.Pi * cutoff / b.rate)
		omega := 2 * math.Pi * freq / b.rate
		// Compensate reflection-filter phase delay at the requested fundamental.
		phaseDelay := math.Atan2(pole*math.Sin(omega), 1-pole*math.Cos(omega)) / omega
		length := b.rate/freq - phaseDelay
		position := 0.07 + 0.32*b.control.Position
		bridge := b.bridge.read(length * position)
		b.loss = (1-pole)*bridge + pole*b.loss
		refBridge := -(0.997 - 0.018*b.control.Damping) * b.loss
		refNut := -0.999 * b.neck.read(length*(1-position))
		bowVelocity := b.envelope * (0.08 + 0.18*b.control.Pressure)
		relative := bowVelocity - refBridge - refNut
		friction := 0.0
		if b.envelope > 0.00001 && b.control.Pressure > 0.0001 {
			u := 0.75 + math.Abs(relative)*(6-4.5*b.control.Pressure)
			friction = math.Min(0.98, 1/(u*u*u*u))
			friction *= math.Min(1, b.envelope/0.008) * math.Min(1, b.control.Pressure/0.02)
		}
		force := relative * friction
		// Last-resort bounds are outside normal operating amplitude.
		b.bridge.push(bowBound(refNut+force, -4, 4))
		b.neck.push(bowBound(refBridge+force, -4, 4))
		x := 0.65*bridge + 0.32*b.body[0].tick(bridge) + 0.22*b.body[1].tick(bridge) + 0.12*b.body[2].tick(bridge)
		// Remove DC generated by bow direction, preserving low string fundamentals.
		y := x - b.dcIn + math.Exp(-2*math.Pi*15/b.rate)*b.dcOut
		b.dcIn = x
		b.dcOut = y
		b.history[b.index] = y
		b.index = (b.index + 1) % len(b.history)
	}
	y := 0.0
	j := b.index
	for _, h := range b.fir {
		j--
		if j < 0 {
			j = len(b.history) - 1
		}
		y += h * b.history[j]
	}
	return 2.2 * y
}
