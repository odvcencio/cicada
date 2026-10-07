// Package clav implements an eight-voice struck-string keyboard with tangent
// contact, damping yarn and two spatial electromagnetic pickups.
package clav

import "math"

const (
	MinNote   = 21
	MaxNote   = 108
	MaxVoices = 8
	modes     = 24
)

type Error string

func (e Error) Error() string { return string(e) }

// Pickup selects the bridge pickup, neck pickup, sum or difference.
type Pickup uint8

const (
	Bridge Pickup = iota
	Neck
	Both
	Difference
)

type Params struct {
	Pickup                            Pickup
	Mute, Tangent, Click, Drive, Gain float64
}

func DefaultParams() Params { return Params{Pickup: Both, Tangent: .18, Click: .3, Gain: .7} }
func Patch(name string) (Params, error) {
	p := DefaultParams()
	switch name {
	case "clav":
	case "clav_muted":
		p.Mute = .8
		p.Pickup = Bridge
	case "clav_hollow":
		p.Pickup = Difference
	default:
		return p, Error("unknown clav patch")
	}
	return p, nil
}

type mode struct{ c, s, r, release, impulse, a, b float32 }
type state struct{ q, p float32 }
type key struct {
	modes           [modes]mode
	count, lifetime int
}
type voice struct {
	key          *key
	note         uint8
	held, active bool
	state        [modes]state
	age          int
	click, gain  float32
	rng          uint32
	last         float32
}
type Instrument struct {
	params                                                               Params
	rate                                                                 int
	keys                                                                 [MaxNote - MinNote + 1]key
	voices                                                               [MaxVoices]voice
	pedal                                                                bool
	serial                                                               uint32
	dcPole, dcIn, dcOut, lowAlpha, low, clickR, drive, gain, tail, tailR float32
	voiceLimit                                                           int
}

func New(rate int, p Params) (*Instrument, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 && rate != 192000 {
		return nil, Error("unsupported clav rate")
	}
	if p.Pickup > Difference {
		return nil, Error("invalid clav pickup")
	}
	for _, x := range []float64{p.Mute, p.Tangent, p.Click, p.Drive, p.Gain} {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, Error("nonfinite clav parameter")
		}
	}
	if p.Mute < 0 || p.Mute > 1 || p.Tangent < .05 || p.Tangent > .45 || p.Click < 0 || p.Click > 1 || p.Drive < 0 || p.Drive > 1 || p.Gain < 0 || p.Gain > 2 {
		return nil, Error("clav parameter outside range")
	}
	i := &Instrument{params: p, rate: rate, drive: float32(1 + 4*p.Drive), gain: float32(.55 * p.Gain)}
	fs := float64(rate)
	i.dcPole = float32(math.Exp(-2 * math.Pi * 25 / fs))
	i.lowAlpha = float32(1 - math.Exp(-2*math.Pi*min(11000., fs*.22)/fs))
	i.clickR = float32(math.Exp(-1 / (.0012 * fs)))
	i.tailR = float32(math.Exp(-1 / (.003 * fs)))
	for note := MinNote; note <= MaxNote; note++ {
		k := &i.keys[note-MinNote]
		f := 440 * math.Exp2(float64(note-69)/12)
		t60 := (2.4 - 2*p.Mute) * math.Exp2(float64(60-note)/48)
		k.lifetime = int(min(12., t60*1.5) * fs)
		for m := 1; m <= modes; m++ {
			h := float64(m)
			hz := f * h * math.Sqrt((1+.000015*h*h)/(1+.000015))
			if hz > .42*fs {
				break
			}
			a := 2 * math.Pi * hz / fs
			// Tangent impulse excites string modes at its contact point. Yarn
			// damps short wavelengths more strongly, rather than lowering gain.
			decay := t60 / (1 + (.014+.09*p.Mute)*h*h)
			k.modes[k.count] = mode{c: float32(math.Cos(a)), s: float32(math.Sin(a)), r: float32(math.Exp(-6.907755 / (decay * fs))), release: float32(math.Exp(-6.907755 / (.035 * fs))), impulse: float32(math.Sin(math.Pi*h*p.Tangent) / h), a: float32(math.Sin(math.Pi * h * .12)), b: float32(math.Sin(math.Pi * h * .72))}
			k.count++
		}
	}
	i.voiceLimit = MaxVoices
	return i, nil
}
func (i *Instrument) NoteOn(note, velocity uint8) error {
	if note < MinNote || note > MaxNote || velocity < 1 || velocity > 127 {
		return Error("invalid clav note or velocity")
	}
	slot := 0
	old := -1
	for j := range i.voices[:i.voiceLimit] {
		if !i.voices[j].active {
			slot = j
			old = -2
			break
		}
		if i.voices[j].age > old {
			old = i.voices[j].age
			slot = j
		}
	}
	if old >= 0 {
		i.tail += i.voices[slot].last
	}
	i.serial++
	v := &i.voices[slot]
	*v = voice{key: &i.keys[int(note)-MinNote], note: note, active: true, held: true, rng: 0xa341316c ^ i.serial ^ uint32(note)*7919}
	x := float32(velocity) / 127
	v.gain = float32(.15 + float32(.85*x))
	v.click = float32(float32(i.params.Click) * float32(x*.009))
	for m := 0; m < v.key.count; m++ {
		h := float32(m + 1)
		felt := float32(1) / float32(1+float32(float32(.006*float32(1-x))*float32(h*h)))
		v.state[m].p = float32(v.key.modes[m].impulse * felt)
	}
	return nil
}
func (i *Instrument) NoteOff(note uint8) {
	for j := range i.voices {
		v := &i.voices[j]
		if v.active && v.held && v.note == note {
			v.held = false
			v.click += float32(float32(i.params.Click) * .025)
		}
	}
}
func (i *Instrument) AllNotesOff() {
	for j := range i.voices {
		i.NoteOff(i.voices[j].note)
	}
}
func (i *Instrument) SetSustain(x float32) error {
	if math.IsNaN(float64(x)) || x < 0 || x > 1 {
		return Error("invalid clav sustain")
	}
	i.pedal = x >= .5
	return nil
}
func (i *Instrument) Reset() {
	i.voices = [MaxVoices]voice{}
	i.pedal = false
	i.serial = 0
	i.low = 0
	i.dcIn = 0
	i.dcOut = 0
	i.tail = 0
}
func (i *Instrument) Active() int {
	n := 0
	for j := range i.voices {
		if i.voices[j].active {
			n++
		}
	}
	return n
}
func sat(x float32) float32 {
	if x > 3 {
		return 1
	}
	if x < -3 {
		return -1
	}
	x2 := float32(x * x)
	return float32(x*float32(27+x2)) / float32(27+float32(9*x2))
}
func (i *Instrument) NextStereo() (float32, float32) {
	sum := float32(0)
	for j := range i.voices {
		v := &i.voices[j]
		if !v.active {
			continue
		}
		a, b := float32(0), float32(0)
		for m := 0; m < v.key.count; m++ {
			s := &v.state[m]
			c := &v.key.modes[m]
			q := float32(c.c*s.q) + float32(c.s*s.p)
			p := float32(c.c*s.p) - float32(c.s*s.q)
			d := c.r
			if !v.held && !i.pedal {
				d = float32(d * c.release)
			}
			s.q = float32(q * d)
			s.p = float32(p * d)
			// The electromagnetic pickups measure changing flux. Modal
			// velocity weights short wavelengths at the attack; the yarn
			// then removes their energy faster than the fundamental.
			velocity := float32(s.p * float32(m+1))
			a += float32(velocity * c.a)
			b += float32(velocity * c.b)
		}
		y := a
		switch i.params.Pickup {
		case Neck:
			y = b
		case Both:
			y = float32(.5 * float32(a+b))
		case Difference:
			y = float32(.5 * float32(a-b))
		}
		v.rng ^= v.rng << 13
		v.rng ^= v.rng >> 17
		v.rng ^= v.rng << 5
		y = float32(y*v.gain) + float32(float32(int32(v.rng))/2147483648*v.click)
		v.click = float32(v.click * i.clickR)
		v.last = y
		sum += y
		v.age++
		if v.age > v.key.lifetime || !v.held && !i.pedal && math.Abs(float64(v.state[0].q))+math.Abs(float64(v.state[0].p)) < 1e-7 {
			v.active = false
		}
	}
	sum += i.tail
	i.tail = float32(i.tail * i.tailR)
	i.low += float32(i.lowAlpha * float32(sum-i.low))
	y := sat(float32(i.low*i.drive)) / i.drive
	dc := float32(y-i.dcIn) + float32(i.dcPole*i.dcOut)
	i.dcIn = y
	i.dcOut = dc
	dc = float32(dc * i.gain)
	return dc, dc
}

// SetVoiceLimit configures bounded polyphony before playback. Reducing it
// clears the removed slots; Reset retains the limit.
func (i *Instrument) SetVoiceLimit(limit int) error {
	if limit < 1 || limit > MaxVoices {
		return Error("keyboard voice limit must be 1 to 8")
	}
	for j := limit; j < MaxVoices; j++ {
		i.voices[j] = voice{}
	}
	i.voiceLimit = limit
	return nil
}
