// Package piano implements a bounded modal grand piano without sample assets.
// One audio owner must serialize note, pedal and render calls.
package piano

import "math"

const (
	MinNote      = 21
	MaxNote      = 108
	MaxVoices    = 8
	MaxModes     = 48
	keyCount     = MaxNote - MinNote + 1
	contactSteps = 4
)

type Error string

func (e Error) Error() string { return string(e) }

// A mode uses displacement and quadrature displacement. Its damped rotation
// is the exact free solution of a lossy oscillator; no Euler string update is
// used. Explicit float32 products fix the native/WASM rounding boundaries.
type modeCoefficients struct {
	c, s, decay, subC, subS, subDecay float32
	strike, impulse, bridge           float32
}
type modeState struct{ q, p float32 }

type key struct {
	modes                                       [MaxModes]modeCoefficients
	count                                       int
	inverseHammerMass, stiffness, velocity, pan float32
	damper                                      float32
	compliance                                  float32
	lifetime                                    int
}

type voice struct {
	state                          [MaxModes]modeState
	key                            *key
	note                           uint8
	held, contact, touched         bool
	age                            int
	hammerPosition, hammerVelocity float32
	damping, last                  float32
}

type resonator struct {
	c, s, decay, damped, gain, q, p float32
}

func (r *resonator) next(input, damping float32) float32 {
	p := r.p + float32(input*r.gain)
	q := float32(r.c*r.q) + float32(r.s*p)
	p = float32(r.c*p) - float32(r.s*r.q)
	r.q, r.p = float32(q*damping), float32(p*damping)
	return r.q
}

// Instrument has eight struck-note slots, a shared bank of unstruck strings,
// and a shared soundboard. New prepares all 88 keys. NoteOn, NoteOff,
// SetSustain and NextStereo allocate nothing and do no coefficient design.
type Instrument struct {
	rate                                        int
	keys                                        [keyCount]key
	voices                                      [MaxVoices]voice
	sympathetic                                 [keyCount]resonator
	board                                       [12]resonator
	held                                        [keyCount]uint8
	pedal, pedalTarget, pedalAlpha, damperAlpha float32
	hammerStep, dcPole, lowAlpha                float32
	dcInL, dcInR, dcOutL, dcOutR, lowL, lowR    float32
	tailL, tailR, tailDecay                     float32
	tailFrames, stealFrames                     int
}

func New(sampleRate int) (*Instrument, error) {
	if sampleRate != 44100 && sampleRate != 48000 && sampleRate != 96000 {
		return nil, Error("piano sample rate must be 44100, 48000, or 96000")
	}
	p := &Instrument{rate: sampleRate}
	fs := float64(sampleRate)
	p.hammerStep = float32(1 / (fs * contactSteps))
	p.pedalAlpha = float32(1 - math.Exp(-1/(.004*fs)))
	p.damperAlpha = float32(1 - math.Exp(-1/(.006*fs)))
	p.dcPole = float32(math.Exp(-2 * math.Pi * 12 / fs))
	p.lowAlpha = float32(1 - math.Exp(-2*math.Pi*9000/fs))
	p.stealFrames = sampleRate / 250
	p.tailDecay = float32(math.Exp(-8 / float64(p.stealFrames)))
	for i := range p.keys {
		p.prepareKey(i)
		f := noteFrequency(i + MinNote)
		p.sympathetic[i] = newResonator(f, 1.8+2.2/(1+f/400), fs, .000045)
		p.sympathetic[i].damped = float32(math.Exp(-1 / (.025 * fs)))
	}
	frequencies := [...]float64{73, 109, 164, 233, 349, 523, 784, 1174, 1760, 2640, 3960, 5940}
	for i, f := range frequencies {
		// Broad, lossy plate modes: an analytic soundboard, with no recorded IR.
		p.board[i] = newResonator(f, 1/(math.Pi*(18+.22*f)), fs, .012)
	}
	return p, nil
}

func noteFrequency(note int) float64 { return 440 * math.Exp2(float64(note-69)/12) }

// Stiffness returns the dimensionless bending-stiffness coefficient B. The
// bass and treble curves are design parameters, not a fit to a sampled grand.
func Stiffness(note uint8) float64 {
	n := float64(note)
	if n < 48 {
		return .00012 + .00018*math.Exp2((36-n)/12)
	}
	return .00012 + .00016*math.Exp2((n-60)/12)
}

// PartialFrequency is the normalized stiff-string dispersion law. Partial 1
// remains at equal temperament; higher partials stretch with n squared.
func PartialFrequency(note uint8, partial int) float64 {
	if note < MinNote || note > MaxNote || partial < 1 || partial > MaxModes {
		return 0
	}
	b := Stiffness(note)
	n := float64(partial)
	return noteFrequency(int(note)) * n * math.Sqrt((1+b*n*n)/(1+b))
}

func (p *Instrument) prepareKey(index int) {
	note := index + MinNote
	f := noteFrequency(note)
	fs := float64(p.rate)
	k := &p.keys[index]
	strings := 3
	if note < 33 {
		strings = 1
	} else if note < 48 {
		strings = 2
	}
	position := .115 + .025*math.Exp2(float64(48-note)/24)/(1+math.Exp2(float64(48-note)/24))
	// String/hammer masses scale through the keyboard. These are deliberately
	// smooth design curves, avoiding sample-key discontinuities.
	mass := .0045 * math.Exp2(float64(60-note)/18)
	hammerMass := .012 - .007*float64(index)/float64(keyCount-1)
	k.inverseHammerMass = float32(1 / hammerMass)
	k.stiffness = float32(3.5e11 * math.Exp2(float64(note-60)/24))
	k.velocity = float32(.7 + .6*float64(index)/float64(keyCount-1))
	k.pan = float32(.18 * float64(note-64) / 44)
	t60 := 8.0 * math.Exp2(float64(60-note)/28)
	k.damper = float32(math.Exp(-6.907755 / (.14 * fs)))
	k.lifetime = int(min(28., t60*1.5) * fs)
	k.compliance = float32(float32(p.hammerStep*p.hammerStep) * k.inverseHammerMass)
	for partial := 1; partial <= MaxModes/strings; partial++ {
		for stringIndex := 0; stringIndex < strings; stringIndex++ {
			detune := 0.
			if strings > 1 {
				detune = (float64(stringIndex) - float64(strings-1)*.5) * 1.4
			}
			hz := PartialFrequency(uint8(note), partial) * math.Exp2(detune/1200)
			if hz >= .42*fs {
				continue
			}
			omega := 2 * math.Pi * hz
			// Higher partials lose energy sooner; unison beating survives into
			// the aftersound. The loss is applied to the exact modal rotation.
			tau := t60 / 6.907755 / (1 + .025*float64(partial*partial) + hz/16000)
			rho := math.Exp(-1 / (tau * fs))
			angle := omega / fs
			strike := math.Sin(math.Pi * float64(partial) * position)
			sign := 1.
			if partial%2 == 0 {
				sign = -1
			}
			k.modes[k.count] = modeCoefficients{
				c: float32(math.Cos(angle)), s: float32(math.Sin(angle)), decay: float32(rho),
				subC: float32(math.Cos(angle / contactSteps)), subS: float32(math.Sin(angle / contactSteps)), subDecay: float32(math.Exp(-1 / (tau * fs * contactSteps))),
				strike: float32(strike), impulse: float32(2 * strike / (mass * omega * fs * contactSteps)),
				// Finite plate radiation falls below its first broad modes. This
				// suppresses subsonic/very low bridge force without removing the
				// bass string's audible upper-partial structure.
				bridge: float32(sign * f * float64(partial) * .18 / float64(strings) * hz * hz / (hz*hz + 110*110)),
			}
			k.count++
			m := &k.modes[k.count-1]
			k.compliance += float32(float32(float32(m.strike*m.subS)*m.impulse) * m.subDecay)
		}
	}
}

func newResonator(f, tau, rate, gain float64) resonator {
	angle := 2 * math.Pi * f / rate
	return resonator{c: float32(math.Cos(angle)), s: float32(math.Sin(angle)), decay: float32(math.Exp(-1 / (tau * rate))), gain: float32(gain * 48000 / rate)}
}

func (p *Instrument) NoteOn(note, velocity uint8) error {
	if note < MinNote || note > MaxNote || velocity > 127 {
		return Error("piano note or velocity is out of range")
	}
	if velocity == 0 {
		p.NoteOff(note)
		return nil
	}
	index := -1
	// Retrigger the same key, retaining its vibrating string state and using
	// another hammer impact rather than summing duplicate virtual strings.
	for i := range p.voices {
		if p.voices[i].key != nil && p.voices[i].note == note {
			index = i
			break
		}
	}
	if index < 0 {
		for i := range p.voices {
			if p.voices[i].key == nil {
				index = i
				break
			}
		}
	}
	if index < 0 {
		index = 0
		for i := 1; i < MaxVoices; i++ {
			v, best := &p.voices[i], &p.voices[index]
			if !v.held && best.held || v.held == best.held && v.age > best.age {
				index = i
			}
		}
	}
	v := &p.voices[index]
	if v.key != nil && v.note != note {
		pan := v.key.pan
		p.tailL += float32(v.last * (1 - pan))
		p.tailR += float32(v.last * (1 + pan))
		p.tailFrames = p.stealFrames
		if v.held {
			p.held[v.note-MinNote] = 0
		}
		*v = voice{}
	}
	v.key, v.note, v.held, v.contact, v.touched, v.age = &p.keys[note-MinNote], note, true, true, false, 0
	p.held[note-MinNote] = 1
	var displacement float32
	for i := 0; i < v.key.count; i++ {
		displacement += float32(v.state[i].q * v.key.modes[i].strike)
	}
	// Start clear of the *moving* string. An absolute restart would teleport
	// the felt into an existing excursion and inject spurious spring energy.
	v.hammerPosition = displacement - .00015
	x := float32(velocity) / 127
	v.hammerVelocity = v.key.velocity + float32(4.8*float32(x*x))
	v.damping = 1
	return nil
}

func (p *Instrument) NoteOff(note uint8) {
	if note < MinNote || note > MaxNote {
		return
	}
	p.held[note-MinNote] = 0
	for i := range p.voices {
		if p.voices[i].note == note {
			p.voices[i].held = false
		}
	}
}

func (p *Instrument) AllNotesOff() {
	clear(p.held[:])
	for i := range p.voices {
		p.voices[i].held = false
	}
}

// SetSustain supports continuous half pedaling. Zero lowers the dampers; one
// raises them. Upper keys (MIDI 89 and above) have no dampers, as on a grand.
func (p *Instrument) SetSustain(value float32) error {
	if value != value || value < 0 || value > 1 {
		return Error("piano sustain must be between zero and one")
	}
	p.pedalTarget = value
	return nil
}

func (p *Instrument) ActiveVoices() int {
	n := 0
	for i := range p.voices {
		if p.voices[i].key != nil {
			n++
		}
	}
	return n
}

// hammer advances a one-sided cubic felt spring against the actual summed
// string displacement. Its reaction slows and rebounds the hammer. Four
// substeps are used only while the hammer is in flight/contact.
func (p *Instrument) hammer(v *voice) {
	k := v.key
	for sub := 0; sub < contactSteps; sub++ {
		var displacement float32
		for i := 0; i < k.count; i++ {
			m, s := &k.modes[i], &v.state[i]
			free := float32(float32(m.subC*s.q)+float32(m.subS*s.p)) * m.subDecay
			displacement += float32(free * m.strike)
		}
		freeHammer := v.hammerPosition + float32(v.hammerVelocity*p.hammerStep)
		compression := max(float32(0), freeHammer-displacement)
		// Implicit one-sided contact: c + K C c^3 = c_free. C contains
		// hammer and string compliance at this substep. Newton starts above
		// the root and decreases monotonically. Bounding the final impulse
		// prevents explicit-contact blowups on vibrating-key retriggers.
		c := compression
		z := float32(k.stiffness * k.compliance)
		for iteration := 0; iteration < 8; iteration++ {
			u := float32(z * float32(c*c))
			c -= (c + float32(u*c) - compression) / (1 + float32(3*u))
		}
		force := min(float32(float32(k.stiffness*float32(c*c))*c), compression/k.compliance)
		if compression > 0 {
			v.touched = true
		}
		v.hammerVelocity -= float32(float32(force*k.inverseHammerMass) * p.hammerStep)
		v.hammerPosition = freeHammer - float32(float32(float32(force*k.inverseHammerMass)*p.hammerStep)*p.hammerStep)
		for i := 0; i < k.count; i++ {
			m, s := &k.modes[i], &v.state[i]
			a := s.p + float32(force*m.impulse)
			q := float32(m.subC*s.q) + float32(m.subS*a)
			a = float32(m.subC*a) - float32(m.subS*s.q)
			s.q, s.p = float32(q*m.subDecay), float32(a*m.subDecay)
		}
	}
	if v.touched && v.hammerVelocity < 0 && v.hammerPosition < 0 || v.age > p.rate/40 {
		v.contact = false
	}
}

func (p *Instrument) NextStereo() (float32, float32) {
	p.pedal += float32((p.pedalTarget - p.pedal) * p.pedalAlpha)
	var left, right float32
	for i := range p.voices {
		v := &p.voices[i]
		if v.key == nil {
			continue
		}
		k := v.key
		if v.contact {
			p.hammer(v)
		} else {
			for j := 0; j < k.count; j++ {
				m, s := &k.modes[j], &v.state[j]
				q := float32(m.c*s.q) + float32(m.s*s.p)
				a := float32(m.c*s.p) - float32(m.s*s.q)
				rho := float32(m.decay * v.damping)
				s.q, s.p = float32(q*rho), float32(a*rho)
			}
		}
		damping := float32(1)
		if !v.held && v.note < 89 {
			damping = k.damper + float32((1-k.damper)*p.pedal)
		}
		v.damping += float32((damping - v.damping) * p.damperAlpha)
		var bridge float32
		for j := 0; j < k.count; j++ {
			bridge += float32(v.state[j].q * k.modes[j].bridge)
		}
		v.last = bridge
		left += float32(bridge * (1 - k.pan))
		right += float32(bridge * (1 + k.pan))
		v.age++
		if v.age > k.lifetime || !v.held && v.note < 89 && p.pedal < .001 && v.age > p.rate && abs(bridge) < 1e-7 {
			v.key = nil
		}
	}
	if p.tailFrames > 0 {
		left += p.tailL
		right += p.tailR
		p.tailL, p.tailR = float32(p.tailL*p.tailDecay), float32(p.tailR*p.tailDecay)
		p.tailFrames--
	}
	bridge := float32((left + right) * .5)
	var resonance float32
	for i := range p.sympathetic {
		r := &p.sympathetic[i]
		lift := p.pedal
		if p.held[i] != 0 || i+MinNote >= 89 {
			lift = 1
		}
		damping := r.damped + float32((r.decay-r.damped)*lift)
		resonance += r.next(bridge, damping)
	}
	left += float32(resonance * .055)
	right += float32(resonance * .06)
	for i := range p.board {
		r := &p.board[i]
		y := r.next(bridge, r.decay)
		if i%2 == 0 {
			left += float32(y * .16)
			right += float32(y * .11)
		} else {
			left += float32(y * .11)
			right += float32(y * .16)
		}
	}
	// Broadband plate radiation plus low modal mobility, with bounded bandwidth
	// and a DC blocker. The shared plate keeps ringing across note releases.
	p.lowL += float32((left - p.lowL) * p.lowAlpha)
	p.lowR += float32((right - p.lowR) * p.lowAlpha)
	left = p.lowL - p.dcInL + float32(p.dcPole*p.dcOutL)
	right = p.lowR - p.dcInR + float32(p.dcPole*p.dcOutR)
	p.dcInL, p.dcInR, p.dcOutL, p.dcOutR = p.lowL, p.lowR, left, right
	return left, right
}

func abs(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// Reset clears ringing strings, the soundboard and the pedal without redesign.
func (p *Instrument) Reset() {
	clear(p.voices[:])
	clear(p.held[:])
	for i := range p.sympathetic {
		p.sympathetic[i].q, p.sympathetic[i].p = 0, 0
	}
	for i := range p.board {
		p.board[i].q, p.board[i].p = 0, 0
	}
	p.pedal, p.pedalTarget = 0, 0
	p.dcInL, p.dcInR, p.dcOutL, p.dcOutR, p.lowL, p.lowR = 0, 0, 0, 0, 0, 0
	p.tailL, p.tailR, p.tailFrames = 0, 0, 0
}
