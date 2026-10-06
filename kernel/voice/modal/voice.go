package modal

import "math"

type Profile uint8

const (
	Steelpan Profile = iota
	Marimba
	Vibraphone
	Conga
	Shaker
	Wood
	Zinc
	Felt
	Marble
	ProfileCount
)

// MaxVoices is the upper bound on simultaneously ringing strikes per Voice.
const MaxVoices = 4

const maxModes = 10

var Names = [ProfileCount]string{
	"steelpan", "marimba", "vibraphone", "conga", "shaker",
	"wood", "zinc", "felt", "marble",
}

func ParseProfile(name string) (Profile, bool) {
	for p, candidate := range Names {
		if candidate == name {
			return Profile(p), true
		}
	}
	return 0, false
}

// ParseTrackKind recognizes the opt-in modeled instrument namespace.
func ParseTrackKind(kind string) (Profile, bool) {
	const prefix = "model_"
	if len(kind) <= len(prefix) || kind[:len(prefix)] != prefix {
		return 0, false
	}
	return ParseProfile(kind[len(prefix):])
}

type Error string

func (e Error) Error() string { return string(e) }

type recipe struct {
	ratio, weight, t60 [maxModes]float64
	count              int
	soft, hard, noise  float64
}

// T60 values are seconds to lose 60 dB of amplitude. Bars emphasize their
// tuned bending modes; membranes and impact surfaces retain inharmonic modes.
// Steelpan's quiet close mode gives a beating sympathetic skirt to the bowl.
var recipes = [ProfileCount]recipe{
	Steelpan: {
		ratio:  [maxModes]float64{1, 1.008, 2, 3, 4.09, 5.41, 6.8, 8.3, 9.6},
		weight: [maxModes]float64{1, .08, .62, .36, .16, .11, .075, .05, .035},
		t60:    [maxModes]float64{2.4, 2.8, 1.8, 1.25, .72, .46, .3, .2, .15},
		count:  9, soft: .00045, hard: .00010, noise: .04,
	},
	Marimba: {
		ratio:  [maxModes]float64{1, 4, 9.9, 17.1, 24.5, 33.2},
		weight: [maxModes]float64{1, .64, .3, .16, .08, .04},
		t60:    [maxModes]float64{1.7, .7, .32, .17, .105, .075},
		count:  6, soft: .00085, hard: .00024, noise: .025,
	},
	Vibraphone: {
		ratio:  [maxModes]float64{1, 4, 9.9, 17.2, 25.4, 35.1},
		weight: [maxModes]float64{1, .62, .24, .12, .06, .03},
		t60:    [maxModes]float64{4.2, 2.6, 1.3, .65, .32, .2},
		count:  6, soft: .00065, hard: .00018, noise: .02,
	},
	Conga: {
		ratio:  [maxModes]float64{1, 1.593, 2.135, 2.296, 2.917, 3.155, 3.6, 4.21},
		weight: [maxModes]float64{1, .76, .52, .31, .23, .19, .14, .10},
		t60:    [maxModes]float64{.62, .4, .29, .22, .18, .13, .1, .075},
		count:  8, soft: .0016, hard: .00032, noise: .35,
	},
	Shaker: {
		ratio:  [maxModes]float64{8, 12, 18, 23, 31, 40},
		weight: [maxModes]float64{.4, .66, 1, .9, .7, .42},
		t60:    [maxModes]float64{.08, .065, .045, .035, .025, .02},
		count:  6, soft: .00024, hard: .00010, noise: 1,
	},
	Wood: {
		ratio:  [maxModes]float64{1, 2.76, 5.4, 8.9, 13.1, 18.2},
		weight: [maxModes]float64{1, .64, .35, .2, .11, .06},
		t60:    [maxModes]float64{.28, .18, .12, .085, .055, .035},
		count:  6, soft: .0005, hard: .00012, noise: .15,
	},
	Zinc: {
		ratio:  [maxModes]float64{1, 1.47, 2.31, 3.07, 4.48, 6.09, 8.4, 11.3, 15.2, 19.7},
		weight: [maxModes]float64{1, .81, .7, .51, .4, .3, .22, .16, .1, .07},
		t60:    [maxModes]float64{1.8, 1.5, 1.25, 1, .8, .65, .48, .32, .22, .15},
		count:  10, soft: .00027, hard: .000085, noise: .2,
	},
	Felt: {
		ratio:  [maxModes]float64{1, 2.2, 4.6, 7.9, 12.3},
		weight: [maxModes]float64{1, .43, .2, .09, .04},
		t60:    [maxModes]float64{.075, .05, .032, .02, .013},
		count:  5, soft: .0017, hard: .0007, noise: .3,
	},
	Marble: {
		ratio:  [maxModes]float64{1, 2.33, 3.76, 5.9, 8.1, 11.6, 15.3, 21.4},
		weight: [maxModes]float64{1, .81, .65, .44, .3, .2, .12, .08},
		t60:    [maxModes]float64{.3, .23, .16, .11, .08, .055, .04, .025},
		count:  8, soft: .00023, hard: .000085, noise: .18,
	},
}

type resonator struct {
	x, y, c, s, gain float64
	freq, radius     float64
	dc, ds           float64
	active           bool
}

type strike struct {
	modes                            [maxModes]resonator
	age, contact, excitation, count  int
	note                             uint8
	active                           bool
	noise                            uint32
	pulseX, pulseY, pulseC, pulseS   float64
	noiseMix, force                  float64
	glide                            int
	grainNext, grainAge, grainLength int
	grainSpacing                     int
}

type Voice struct {
	profile   Profile
	rate      float64
	strikes   [MaxVoices]strike
	sequence  uint32
	latest    int
	stealTail float64
	stealLeft int
}

func NewVoice(profile Profile, sampleRate int) (*Voice, error) {
	if profile >= ProfileCount {
		return nil, Error("modal profile is out of range")
	}
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("modal sample rate must be 44100, 48000, or 96000")
	}
	v := &Voice{profile: profile, rate: float64(sampleRate)}
	v.Reset()
	return v, nil
}

// Reset also resets the deterministic four-strike variation sequence.
func (v *Voice) Reset() {
	v.strikes = [MaxVoices]strike{}
	v.sequence, v.latest, v.stealTail, v.stealLeft = 0, -1, 0, 0
}

func (v *Voice) ActiveVoices() int {
	count := 0
	for i := range v.strikes {
		if v.strikes[i].active {
			count++
		}
	}
	return count
}

func (v *Voice) pitch(note uint8) float64 {
	if v.profile == Shaker {
		// A shaker's vessel colour changes slowly with the authored pitch;
		// its grain modes are not an equal-tempered musical fundamental.
		return 330 * math.Exp2((float64(note)-60)/48)
	}
	return 440 * math.Exp2((float64(note)-69)/12)
}

// NoteOn starts a finite strike. Velocity zero is a key-up, and cannot excite
// a silent voice. A slide on a ringing voice changes poles over 3 ms while
// retaining resonator state, contact age and the strike variation sequence.
func (v *Voice) NoteOn(note, velocity uint8, slide bool) {
	if velocity == 0 {
		v.NoteOff()
		return
	}
	note, velocity = min(note, 127), min(velocity, 127)
	if slide && v.latest >= 0 && v.strikes[v.latest].active {
		v.retune(&v.strikes[v.latest], note)
		return
	}
	index := -1
	quietest := math.Inf(1)
	for i := range v.strikes {
		s := &v.strikes[i]
		if !s.active {
			index = i
			break
		}
		energy := 0.0
		for m := 0; m < s.count; m++ {
			mode := &s.modes[m]
			energy += mode.x*mode.x + mode.y*mode.y
		}
		if energy < quietest {
			quietest, index = energy, i
		}
	}
	s := &v.strikes[index]
	if s.active {
		// A short bounded fade keeps stealing the quietest ringing strike
		// from producing a discontinuous sample at the four-strike limit.
		v.stealTail *= float64(v.stealLeft) / 64
		for m := 0; m < s.count; m++ {
			v.stealTail += s.modes[m].y
		}
		v.stealLeft = 64
	}
	*s = strike{active: true, note: note}
	r := &recipes[v.profile]
	variation := v.sequence & 3
	v.sequence++
	s.noise = 0x6d2b79f5 ^ uint32(note+1)*0x9e3779b9 ^ (variation+1)*0x85ebca6b
	vel := float64(velocity) / 127
	// This mildly curved response leaves headroom for overlapping strikes.
	level := .9 * vel * math.Sqrt(vel)
	hardness := vel * vel
	contact := (r.soft + (r.hard-r.soft)*hardness) * (1 + .045*(float64(variation)-1.5))
	s.contact = max(4, int(math.Round(contact*v.rate)))
	s.excitation = s.contact
	s.force, s.noiseMix = 2/float64(s.contact), r.noise
	angle := 2 * math.Pi / float64(s.contact)
	s.pulseS, s.pulseC = math.Sincos(angle)
	s.pulseY, s.pulseX = math.Sincos(angle / 2)
	frequency := v.pitch(note)
	weightSum := 0.0
	for m := 0; m < r.count; m++ {
		if frequency*r.ratio[m] < .45*v.rate {
			weightSum += r.weight[m]
		}
	}
	for m := 0; m < r.count; m++ {
		freq := frequency * r.ratio[m]
		if freq >= .45*v.rate {
			continue
		}
		mode := &s.modes[s.count]
		mode.freq, mode.active = freq, true
		mode.radius = math.Exp(-math.Log(1000) / (r.t60[m] * v.rate))
		mode.s, mode.c = math.Sincos(2 * math.Pi * freq / v.rate)
		mode.c, mode.s = mode.c*mode.radius, mode.s*mode.radius
		brightness := 1.0
		if m != 0 {
			brightness = .25 + .75*hardness
		}
		// Different virtual contact positions give four timbres. Frequencies
		// and decay poles remain identical for every strike position.
		position := 1 + .055*math.Sin(float64((m+1)*int(variation+1))*1.7)
		mode.gain = level * r.weight[m] / weightSum * brightness * position
		s.count++
	}
	if v.profile == Shaker {
		// A bounded train of grain collisions gives an aggregate shake,
		// rather than a single envelope applied to broadband noise.
		s.excitation = int((.045 + .035*vel) * v.rate)
		s.grainSpacing = max(s.contact+1, int(.0027*v.rate))
		s.grainNext, s.grainAge, s.grainLength = 0, -1, s.contact
		s.force *= 1.8
	}
	if s.count == 0 {
		s.active = false
	}
	v.latest = index
}

func (v *Voice) retune(s *strike, note uint8) {
	oldFrequency, newFrequency := v.pitch(s.note), v.pitch(note)
	ratio := newFrequency / oldFrequency
	s.glide = max(1, int(.003*v.rate))
	for i := 0; i < s.count; i++ {
		m := &s.modes[i]
		m.freq *= ratio
		if m.freq >= .45*v.rate {
			// Omitted modes cannot return through a slide; a new strike can
			// create them again when the pitch returns to the passband.
			m.active, m.x, m.y = false, 0, 0
			continue
		}
		sin, cos := math.Sincos(2 * math.Pi * m.freq / v.rate)
		m.dc = (m.radius*cos - m.c) / float64(s.glide)
		m.ds = (m.radius*sin - m.s) / float64(s.glide)
	}
	s.note = note
}

// NoteOff models a vibraphone damper. Other surfaces continue ringing.
func (v *Voice) NoteOff() {
	if v.profile != Vibraphone {
		return
	}
	radius := math.Exp(-math.Log(1000) / (.07 * v.rate))
	for i := range v.strikes {
		s := &v.strikes[i]
		s.excitation, s.glide = 0, 0
		for j := 0; j < s.count; j++ {
			m := &s.modes[j]
			m.c, m.s = m.c*radius/m.radius, m.s*radius/m.radius
			m.radius, m.dc, m.ds = radius, 0, 0
		}
	}
}

func random(s *strike) float64 {
	x := s.noise
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	s.noise = x
	return float64(int32(x)) / 2147483648
}

func (v *Voice) drive(s *strike) float64 {
	if s.age >= s.excitation {
		return 0
	}
	if v.profile == Shaker {
		if s.age >= s.grainNext {
			s.grainAge = 0
			jitter := int(random(s) * .3 * float64(s.grainSpacing))
			s.grainNext = s.age + s.grainSpacing + jitter
		}
		if s.grainAge < 0 || s.grainAge >= s.grainLength {
			return 0
		}
		x := (float64(s.grainAge) + .5) / float64(s.grainLength)
		s.grainAge++
		return 4 * x * (1 - x) * random(s) * s.force
	}
	pulse := .5 * (1 - s.pulseX)
	x := s.pulseC*s.pulseX - s.pulseS*s.pulseY
	s.pulseY = s.pulseS*s.pulseX + s.pulseC*s.pulseY
	s.pulseX = x
	return pulse * s.force * (1 - s.noiseMix + s.noiseMix*random(s))
}

func (v *Voice) Next() float32 {
	output := 0.0
	for i := range v.strikes {
		s := &v.strikes[i]
		if !s.active {
			continue
		}
		excitation := v.drive(s)
		alive := false
		for j := 0; j < s.count; j++ {
			m := &s.modes[j]
			if !m.active {
				continue
			}
			if s.glide > 0 {
				m.c += m.dc
				m.s += m.ds
			}
			x := m.c*m.x - m.s*m.y + excitation*m.gain
			m.y = m.s*m.x + m.c*m.y
			m.x = x
			// Truncate far below the float32 noise floor before a denormal
			// tail can consume CPU indefinitely.
			if s.age >= s.excitation && math.Abs(m.x)+math.Abs(m.y) < 1e-10 {
				m.x, m.y, m.active = 0, 0, false
				continue
			}
			output += m.y
			alive = true
		}
		if s.glide > 0 {
			s.glide--
		}
		s.age++
		s.active = alive || s.age < s.excitation
	}
	if v.stealLeft > 0 {
		output += v.stealTail * float64(v.stealLeft) / 64
		v.stealLeft--
		if v.stealLeft == 0 {
			v.stealTail = 0
		}
	}
	return float32(output)
}
