// Package ep models struck tine/tonebar and clamped-reed electric keyboards.
// All coefficients and storage are prepared before the audio owner starts.
package ep

import "math"

const (
	MinNote   = 21
	MaxNote   = 108
	MaxVoices = 8
	modeCount = 6
)

type Error string

func (e Error) Error() string { return string(e) }

type Model uint8

const (
	Tine Model = iota
	Reed
)

// Params uses normalized physical controls. TremoloRate is Hz; Decay is the
// fundamental T60 in seconds at middle C. Oversample is 2 or 4.
type Params struct {
	Model                                                  Model
	PickupPosition, PickupDistance, HammerFelt             float64
	Decay, Release, Drive, Tremolo, TremoloRate, Pan, Gain float64
	Oversample                                             int
}

func DefaultParams() Params {
	return Params{Model: Tine, PickupPosition: .32, PickupDistance: .8, HammerFelt: .6, Decay: 20, Release: .12, TremoloRate: 4.6, Gain: .7, Oversample: 2}
}
func Patch(name string) (Params, error) {
	p := DefaultParams()
	switch name {
	case "tine_ep":
	case "tine_bell":
		p.PickupDistance = 1.3
		p.HammerFelt = .34
		p.PickupPosition = .2
	case "tine_bark":
		p.PickupDistance = .48
		p.Drive = .22
		p.HammerFelt = .48
	case "tine_tremolo":
		p.Tremolo = .5
		p.Pan = .8
	case "reed_ep":
		p.Model = Reed
		p.Decay = 3.4
		p.PickupDistance = .68
		p.PickupPosition = .45
		p.Drive = .12
		p.HammerFelt = .46
	case "reed_tremolo":
		p.Model = Reed
		p.Decay = 3.4
		p.PickupDistance = .68
		p.Tremolo = .48
		p.Drive = .2
	default:
		return p, Error("unknown electric piano patch")
	}
	return p, nil
}

type rotation struct{ c, s, r, weight float32 }
type state struct{ q, p float32 }
type key struct {
	modes               [modeCount]rotation
	release, derivative float32
	velocityLoss        float32
	lifetime            int
}
type voice struct {
	key                                       *key
	note                                      uint8
	held, active                              bool
	modes                                     [modeCount]state
	age                                       int
	lastField, thump, thumpP, click, velocity float32
	strikeDecay                               float32
	rng                                       uint32
}

type Instrument struct {
	params                                    Params
	rate                                      int
	keys                                      [MaxNote - MinNote + 1]key
	voices                                    [MaxVoices]voice
	pedal                                     bool
	serial                                    uint32
	gap, offset, drive, amp, lowAlpha, dcPole float32
	low, dcIn, dcOut                          float32
	thumpC, thumpS, thumpR, clickR            float32
	lfoC, lfoS, lfoQ, lfoP                    float32
	stealL, stealR, stealDecay, lastL, lastR  float32
	voiceLimit                                int
}

func New(rate int, p Params) (*Instrument, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 && rate != 192000 {
		return nil, Error("electric piano sample rate is unsupported")
	}
	if p.Model > Reed || (p.Oversample != 2 && p.Oversample != 4) {
		return nil, Error("invalid electric piano model or oversampling")
	}
	for _, x := range []float64{p.PickupPosition, p.PickupDistance, p.HammerFelt, p.Decay, p.Release, p.Drive, p.Tremolo, p.TremoloRate, p.Pan, p.Gain} {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, Error("nonfinite electric piano parameter")
		}
	}
	if p.PickupPosition < 0 || p.PickupPosition > 1 || p.PickupDistance < .2 || p.PickupDistance > 2 || p.HammerFelt < 0 || p.HammerFelt > 1 || p.Decay < .3 || p.Decay > 20 || p.Release < .02 || p.Release > 2 || p.Drive < 0 || p.Drive > 1 || p.Tremolo < 0 || p.Tremolo > 1 || p.TremoloRate < .1 || p.TremoloRate > 12 || p.Pan < 0 || p.Pan > 1 || p.Gain < 0 || p.Gain > 2 {
		return nil, Error("electric piano parameter outside range")
	}
	i := &Instrument{params: p, rate: rate, gap: float32(p.PickupDistance), offset: float32((p.PickupPosition - .5) * .75), drive: float32(1 + 5*p.Drive), amp: float32(p.Gain * .34), lfoP: 1}
	fs := float64(rate * p.Oversample)
	i.lowAlpha = float32(1 - math.Exp(-2*math.Pi*min(8500., float64(rate)*.2)/float64(rate)))
	i.dcPole = float32(math.Exp(-2 * math.Pi * 18 / float64(rate)))
	a := 2 * math.Pi * 54 / fs
	i.thumpC = float32(math.Cos(a))
	i.thumpS = float32(math.Sin(a))
	i.thumpR = float32(math.Exp(-1 / (.022 * fs)))
	i.clickR = float32(math.Exp(-1 / (.0015 * fs)))
	a = 2 * math.Pi * p.TremoloRate / float64(rate)
	i.lfoC = float32(math.Cos(a))
	i.lfoS = float32(math.Sin(a))
	i.stealDecay = float32(math.Exp(-1 / (.003 * float64(rate))))
	for n := MinNote; n <= MaxNote; n++ {
		k := &i.keys[n-MinNote]
		f := 440 * math.Exp2(float64(n-69)/12)
		ratios := [modeCount]float64{1, 1.0015, 6.267, 17.55, 34.39, 56.84}
		weights := [modeCount]float64{.88, .12, .04, .004, .0005, .0001}
		if p.Model == Reed {
			ratios = [modeCount]float64{1, 6.267, 17.55, 34.39, 56.84, 84.9}
			weights = [modeCount]float64{1, .058, .011, .003, .001, .0003}
		}
		for m, ratio := range ratios {
			hz := f * ratio
			if hz > .42*float64(rate) {
				continue
			}
			a = 2 * math.Pi * hz / fs
			t60 := p.Decay * math.Exp2(float64(60-n)/35) / (1 + .34*float64(m*m))
			if p.Model == Tine && m == 1 {
				t60 *= .82
			}
			if p.Model == Tine && m >= 2 {
				h := float64(m - 2)
				t60 = min(t60, 0.6*math.Exp2(float64(60-n)/50)/(1+.7*h*h))
			}
			k.modes[m] = rotation{c: float32(math.Cos(a)), s: float32(math.Sin(a)), r: float32(math.Exp(-6.907755 / (t60 * fs))), weight: float32(weights[m])}
		}
		if p.Model == Tine {
			k.velocityLoss = float32(6.907755 * .7 / (p.Decay * math.Exp2(float64(60-n)/35) * fs))
		}
		k.release = float32(math.Exp(-6.907755 / (p.Release * fs)))
		k.derivative = float32(fs / (2 * math.Pi * f))
		k.lifetime = int(min(40., p.Decay*math.Exp2(float64(60-n)/35)*1.4) * float64(rate))
	}
	i.voiceLimit = MaxVoices
	return i, nil
}

func (i *Instrument) field(q float32) float32 {
	z := q + i.offset
	if i.params.Model == Reed {
		// Electrostatic capacitance grows as the reed approaches its plate.
		// A finite stop bounds the gap at extreme displacement.
		g := i.gap - float32(q*.72)
		if g < i.gap*.2 {
			g = i.gap * .2
		}
		return float32(i.gap/g) - 1
	}
	// Saturating dipole flux; differentiated below to obtain pickup voltage.
	d := float32(i.gap*i.gap) + float32(z*z)
	return z / float32(math.Sqrt(float64(d)))
}

func (i *Instrument) NoteOn(note, velocity uint8) error {
	if note < MinNote || note > MaxNote || velocity == 0 || velocity > 127 {
		return Error("invalid electric piano note or velocity")
	}
	slot := 0
	oldest := -1
	for n := range i.voices[:i.voiceLimit] {
		if !i.voices[n].active {
			slot = n
			oldest = -2
			break
		}
		if i.voices[n].age > oldest {
			slot = n
			oldest = i.voices[n].age
		}
	}
	if oldest >= 0 {
		i.stealL += float32(i.lastL * .125)
		i.stealR += float32(i.lastR * .125)
	}
	i.serial++
	v := &i.voices[slot]
	*v = voice{key: &i.keys[int(note)-MinNote], note: note, held: true, active: true, rng: 0x9e3779b9 ^ uint32(note)*7919 ^ i.serial}
	x := float32(velocity) / 127
	v.velocity = x
	v.strikeDecay = 1 - float32(float32(x*x)*v.key.velocityLoss)
	strike := float32(.018) + float32(.55*float32(x*x))
	for m := range v.modes {
		brightness := float32(1)
		if m > 1 || i.params.Model == Reed && m > 0 {
			excitation := 1.5 - i.params.HammerFelt
			if i.params.Model == Tine {
				excitation = 1.85 - i.params.HammerFelt
			}
			brightness = float32(.06) + float32(x*x)*float32(excitation)
		}
		v.modes[m].p = float32(strike * brightness)
	}
	v.click = float32(.002 * x * float32(1-i.params.HammerFelt))
	v.lastField = i.field(0)
	return nil
}

func (i *Instrument) NoteOff(note uint8) {
	for n := range i.voices {
		v := &i.voices[n]
		if v.active && v.note == note && v.held {
			v.held = false
			v.thumpP = float32(.025 * v.velocity)
			v.click = float32(.003 * v.velocity)
		}
	}
}
func (i *Instrument) AllNotesOff() {
	for n := range i.voices {
		i.NoteOff(i.voices[n].note)
	}
}
func (i *Instrument) SetSustain(value float32) error {
	if math.IsNaN(float64(value)) || value < 0 || value > 1 {
		return Error("invalid sustain")
	}
	i.pedal = value >= .5
	return nil
}
func (i *Instrument) Reset() {
	i.voices = [MaxVoices]voice{}
	i.low = 0
	i.dcIn = 0
	i.dcOut = 0
	i.stealL = 0
	i.stealR = 0
	i.lastL = 0
	i.lastR = 0
	i.lfoQ = 0
	i.lfoP = 1
	i.serial = 0
	i.pedal = false
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

func saturate(x float32) float32 {
	if x > 3 {
		return 1
	}
	if x < -3 {
		return -1
	}
	xx := float32(x * x)
	return float32(x*float32(27+xx)) / float32(27+float32(9*xx))
}

func (i *Instrument) NextStereo() (float32, float32) {
	sum := float32(0)
	for n := range i.voices {
		v := &i.voices[n]
		if !v.active {
			continue
		}
		k := v.key
		out := float32(0)
		for sub := 0; sub < i.params.Oversample; sub++ {
			q := float32(0)
			for m := range v.modes {
				c := &k.modes[m]
				s := &v.modes[m]
				q1 := float32(c.c*s.q) + float32(c.s*s.p)
				p1 := float32(c.c*s.p) - float32(c.s*s.q)
				d := float32(c.r * v.strikeDecay)
				if !v.held && !i.pedal {
					d = float32(d * k.release)
				}
				s.q = float32(q1 * d)
				s.p = float32(p1 * d)
				q += float32(s.q * c.weight)
			}
			f := i.field(q)
			y := float32(float32(f-v.lastField) * k.derivative)
			v.lastField = f
			// Damper contact has its own low resonant thump and felt noise.
			q1 := float32(i.thumpC*v.thump) + float32(i.thumpS*v.thumpP)
			p1 := float32(i.thumpC*v.thumpP) - float32(i.thumpS*v.thump)
			v.thump = float32(q1 * i.thumpR)
			v.thumpP = float32(p1 * i.thumpR)
			v.rng ^= v.rng << 13
			v.rng ^= v.rng >> 17
			v.rng ^= v.rng << 5
			noise := float32(int32(v.rng)) / 2147483648
			out += y + v.thump + float32(noise*v.click)
			v.click = float32(v.click * i.clickR)
		}
		sum += out / float32(i.params.Oversample)
		v.age++
		if v.age >= k.lifetime || (!v.held && !i.pedal && v.age > i.rate/5 && math.Abs(float64(v.modes[0].q))+math.Abs(float64(v.modes[0].p)) < 1e-7) {
			v.active = false
		}
	}
	i.low += float32(i.lowAlpha * float32(sum-i.low))
	y := saturate(float32(i.low*i.drive)) / i.drive
	dc := float32(y-i.dcIn) + float32(i.dcPole*i.dcOut)
	i.dcIn = y
	i.dcOut = dc
	q := float32(i.lfoC*i.lfoQ) + float32(i.lfoS*i.lfoP)
	p := float32(i.lfoC*i.lfoP) - float32(i.lfoS*i.lfoQ)
	i.lfoQ = q
	i.lfoP = p
	// Normalize the recursive oscillator to keep long renders bounded.
	norm := float32(1.5) - float32(.5*float32(float32(q*q)+float32(p*p)))
	i.lfoQ = float32(q * norm)
	i.lfoP = float32(p * norm)
	trem := float32(1) - float32(float32(i.params.Tremolo)*float32(.5+.5*q))
	pan := float32(float32(i.params.Pan) * float32(i.params.Tremolo) * q)
	y = float32(dc*i.amp) * trem
	l := float32(y*float32(1-pan)) + i.stealL
	r := float32(y*float32(1+pan)) + i.stealR
	i.stealL = float32(i.stealL * i.stealDecay)
	i.stealR = float32(i.stealR * i.stealDecay)
	i.lastL = l
	i.lastR = r
	return l, r
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
