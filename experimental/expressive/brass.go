package expressive

import (
	"m31labs.dev/cicada/kernel/dsp/halfband"
	"math"
)

// Brass is an experimental outward-striking lip reed coupled to a cylindrical
// bore. It uses a damped mass/spring lip, unilateral lip opening, and an implicit
// Bernoulli flow junction, rather than a periodic oscillator excitation.
//
// The round-trip bore reflection and mouthpiece scattering follow the standard
// digital-waveguide formulation. This simplified geometry is not a specific
// trumpet/trombone; the playable qualification range is 110..660 Hz. Calibration
// compensates the measured coupled lip/bore pitch, not simply the delay length.
// See Julius O. Smith, Physical Audio Signal Processing, Brasses:
// https://www.dsprelated.com/freebooks/pasp/Modeling_Lips_Mouthpiece.html .
// Lip closure and optional saturation run at 2x, followed by a halfband FIR;
// this reduces aliasing but is not an alias-free or perceptually validated model.
type Brass struct {
	fs                                      float64
	bore                                    delay
	decimator                               halfband.FIR
	expression                              Expression
	gate                                    bool
	velocity, hz, pressure, envelope, phase float64
	lip1, lip2, flow                        float64
	dcInput, dcOutput, lowpass              float64
	smooth, dcPole                          float64
}

func NewBrass(sampleRate int) *Brass {
	if sampleRate < 8000 || sampleRate > 192000 {
		sampleRate = 48000
	}
	b := &Brass{fs: float64(sampleRate) * 2, hz: 220, velocity: .8, decimator: halfband.New()}
	b.bore.init(int(b.fs/60) + 16)
	b.expression = Expression{PitchHz: 220, Pressure: .75, Position: .5, Brightness: .65, Damping: .3}
	b.pressure = .75
	b.smooth = 1 - math.Exp(-1/(.008*b.fs))
	b.dcPole = math.Exp(-2 * math.Pi * 45 / b.fs)
	return b
}

func (b *Brass) NoteOn(hz, velocity float64) {
	b.expression.PitchHz = clamp(hz, 80, 1000)
	b.velocity = clamp(velocity, 0, 1)
	// Rearticulation closes the tongue but preserves the vibrating bore.
	b.envelope *= .18
	if !b.gate {
		b.hz = b.expression.PitchHz
	}
	b.gate = b.velocity > 0
}
func (b *Brass) NoteOff() { b.gate = false }
func (b *Brass) SetExpression(e Expression) {
	if !finite(e.PitchHz) || e.PitchHz <= 0 {
		e.PitchHz = b.expression.PitchHz
	}
	e.PitchHz = clamp(e.PitchHz, 80, 1000)
	e.Pressure = clamp(e.Pressure, 0, 1)
	e.Position = clamp(e.Position, 0, 1)
	e.Brightness = clamp(e.Brightness, 0, 1)
	e.Vibrato = clamp(e.Vibrato, 0, 60)
	e.Drive = clamp(e.Drive, 0, 1)
	e.Damping = clamp(e.Damping, 0, 1)
	b.expression = e
}
func (b *Brass) Next() float64 {
	a := b.tick()
	return b.decimator.Downsample(a, b.tick())
}
func (b *Brass) tick() float64 {
	e := b.expression
	b.hz += b.smooth * (e.PitchHz - b.hz)
	b.pressure += b.smooth * (e.Pressure - b.pressure)
	target := 0.
	if b.gate {
		target = 1
	}
	rate := .006
	if !b.gate {
		rate = .018 + .035*(1-e.Damping)
	}
	b.envelope += (target - b.envelope) / (rate * b.fs)
	b.phase += 5 / b.fs
	if b.phase >= 1 {
		b.phase -= 1
	}
	hz := b.hz * math.Exp2(e.Vibrato*math.Sin(2*math.Pi*b.phase)/1200)
	// Restrict the blowing regime to the measured stable branch; expressive
	// pressure still changes both the flow nonlinearity and radiation amplitude.
	mouthTarget := .42 + .18*(.4*b.velocity+.6*b.pressure)
	calibration := 1.0878 + .065*(mouthTarget-.42)
	f := hz / calibration
	f = hz / (calibration + 2.45*f/b.fs)
	f = hz / (calibration + 2.45*f/b.fs)
	mouth := mouthTarget * b.envelope
	if b.pressure < .001 {
		mouth *= b.pressure / .001
	}
	incoming := -.96 * b.bore.read(b.fs/(2*f)-1)
	differential := mouth - (b.flow + 2*incoming)
	// Exact damped pole positions for the linear lip mass/spring. Q=4;
	// natural lip frequency is below the playing frequency (outward striking).
	w := 2 * math.Pi * f * .8 / b.fs
	r := math.Exp(-w / 8)
	a := 2 * r * math.Cos(w)
	rr := r * r
	lip := a*b.lip1 - rr*b.lip2 + (1-a+rr)*differential
	b.lip2 = b.lip1
	b.lip1 = lip
	opening := math.Max(0, .15+.5*lip)
	// Solve u = opening*sign(dp)*sqrt(abs(dp)) with dp=P-2*pMinus-u.
	// The rationalized quadratic avoids cancellation at small pressure.
	p := mouth - 2*incoming
	den := math.Sqrt(opening*opening+4*math.Abs(p)) + opening
	flow := 0.
	if den > 1e-15 {
		flow = math.Copysign(2*opening*math.Abs(p)/den, p)
	}
	b.flow = flow
	wave := flow + incoming
	if !finite(wave) || math.Abs(wave) > 32 {
		b.lip1 = 0
		b.lip2 = 0
		b.flow = 0
		flow = 0
		wave = 0
	}
	b.bore.push(wave)
	signal := flow + 2*incoming
	// Bell radiation approximated by DC rejection and a moving spectral tilt.
	hp := signal - b.dcInput + b.dcPole*b.dcOutput
	b.dcInput = signal
	b.dcOutput = hp
	cutoff := 900 + 6500*e.Brightness
	alpha := 1 - math.Exp(-2*math.Pi*cutoff/b.fs)
	b.lowpass += alpha * (hp - b.lowpass)
	radiated := b.lowpass*(.75+.25*e.Position) + (.25*(1-e.Position))*(hp-b.lowpass)
	drive := 1 + 3*e.Drive
	return .52 * b.velocity * b.pressure * math.Tanh(radiated*drive) / drive
}
