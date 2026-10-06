// Package modeledkit synthesizes drum-kit strikes from damped resonances and
// filtered excitation. It contains no recordings, wavetables, or external assets.
package modeledkit

import "math"

type Profile uint8

const (
	Kick Profile = iota
	Snare
	Rimshot
	CrossStick
	TomLow
	TomMid
	TomHigh
	HatClosed
	HatPedal
	HatHalfOpen
	HatOpen
	RideBow
	RideBell
	Crash
	Splash
	ProfileCount
)

var Names = [ProfileCount]string{
	"kick", "snare", "rimshot", "cross_stick", "tom_low", "tom_mid", "tom_high",
	"hat_closed", "hat_pedal", "hat_half_open", "hat_open", "ride_bow", "ride_bell", "crash", "splash",
}

func ParseProfile(name string) (Profile, bool) {
	for i, candidate := range Names {
		if name == candidate {
			return Profile(i), true
		}
	}
	return 0, false
}

func IsHat(profile Profile) bool { return profile >= HatClosed && profile <= HatOpen }

type Error string

func (e Error) Error() string { return string(e) }

// Params controls subsequent strikes. Tune scales resonant frequencies; Decay
// scales their T60 times. Position runs from the center to the edge. Humanize
// adds bounded timbre, strength, and pitch changes without moving event times.
type Params struct {
	Tune, Decay, Position, Humanize float64
}

func DefaultParams() Params { return Params{Tune: 1, Decay: 1, Position: .35, Humanize: .015} }

func (p Params) Validate() error {
	for _, value := range [...]float64{p.Tune, p.Decay, p.Position, p.Humanize} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Error("modeled kit parameters must be finite")
		}
	}
	if p.Tune < .5 || p.Tune > 2 || p.Decay < .25 || p.Decay > 2 || p.Position < 0 || p.Position > 1 || p.Humanize < 0 || p.Humanize > .1 {
		return Error("modeled kit parameter is out of range")
	}
	return nil
}

const maxModes = 16

type recipe struct {
	frequency, t60, level float64
	noise, noiseT60       float64
	low, high             float64
	contact, bend         float64
	count                 int
}

// Frequencies approximate membrane eigenmodes, shell/rim resonances, or a
// deliberately inharmonic plate. These are models, not fitted recording data.
var membraneRatios = [maxModes]float64{1, 1.5933, 2.1355, 2.2954, 2.6531, 2.9173, 3.155, 3.598, 3.652, 4.059}
var membraneWeights = [maxModes]float64{1, .58, .38, .25, .17, .14, .11, .085, .07, .055}
var plateRatios = [maxModes]float64{1, 1.347, 1.773, 2.319, 2.873, 3.519, 4.127, 4.893, 5.771, 6.823, 8.119, 9.773, 11.429, 13.817, 16.373, 19.931}
var plateWeights = [maxModes]float64{.24, .32, .4, .53, .65, .72, .76, .8, .76, .71, .64, .53, .43, .33, .24, .16}
var rimRatios = [maxModes]float64{1, 2.378, 9.189, 14.595, 24.865, 33.514}
var rimWeights = [maxModes]float64{.7, .3, 1, .52, .3, .14}
var stickRatios = [maxModes]float64{1, 2.76, 5.4, 8.9, 13.1, 18.2}
var stickWeights = [maxModes]float64{1, .58, .3, .16, .075, .035}

var recipes = [ProfileCount]recipe{
	Kick:        {56, 1.45, 1.4, .19, .016, 1700, 7500, .0022, .48, 8},
	Snare:       {180, .6, 1.05, .88, .6, 950, 11000, .0011, .11, 10},
	Rimshot:     {185, .42, 1.05, .7, .31, 1400, 12500, .00028, .05, 6},
	CrossStick:  {830, .22, 1.15, .13, .009, 1300, 9500, .00034, 0, 6},
	TomLow:      {98, 1.65, 1.3, .2, .024, 1400, 8000, .0017, .2, 10},
	TomMid:      {139, 1.35, 1.3, .21, .022, 1600, 8500, .0015, .18, 10},
	TomHigh:     {196, 1.08, 1.3, .22, .019, 1800, 9000, .0013, .16, 10},
	HatClosed:   {390, .19, .95, .78, .16, 4400, 15500, .00023, 0, 16},
	HatPedal:    {390, .28, 1.05, .75, .2, 2400, 13000, .0009, 0, 16},
	HatHalfOpen: {390, 1.1, 1, .84, .82, 3500, 14500, .0003, 0, 16},
	HatOpen:     {390, 2.1, 1, .86, 1.6, 2800, 14500, .0003, 0, 16},
	RideBow:     {280, 4.2, 1.05, .38, 1.65, 2600, 13500, .00042, 0, 16},
	RideBell:    {620, 3.5, 1.12, .17, .6, 3200, 15000, .00024, 0, 16},
	Crash:       {310, 4.8, 1.12, .85, 3.1, 1300, 15500, .00037, 0, 16},
	Splash:      {620, 1.55, 1.15, .94, 1.12, 2300, 16500, .00025, 0, 16},
}

type mode struct {
	x, y, c, s, targetC, targetS float64
}

// bandFilter is a pair of low-pass poles followed by two high-pass poles.
// Filtering follows amplitude modulation, so wire/contact excitation does not
// bypass the bandwidth limit. Coefficients are computed only at strike time.
type bandFilter struct {
	lowA, highA, low1, low2, high1, high2 float64
}

func (f *bandFilter) configure(low, high, rate float64) {
	f.lowA = 1 - math.Exp(-2*math.Pi*min(high, .35*rate)/rate)
	f.highA = 1 - math.Exp(-2*math.Pi*min(low, .2*rate)/rate)
}

func (f *bandFilter) next(input float64) float64 {
	f.low1 += f.lowA * (input - f.low1)
	f.low2 += f.lowA * (f.low1 - f.low2)
	f.high1 += f.highA * (f.low2 - f.high1)
	high := f.low2 - f.high1
	f.high2 += f.highA * (high - f.high2)
	return high - f.high2
}

type strike struct {
	modes                   [maxModes]mode
	filter                  bandFilter
	age, contact, remaining int
	count                   int
	noise                   uint32
	noiseAmp, noisePole     float64
	bendAlpha, bodyCoupling float64
	wireFollower, followerA float64
	active                  bool
}

// Voice owns one ringing strike and a bounded 1 ms fade of its predecessor.
// It does not grow polyphony when a lane is retriggered.
type Voice struct {
	profile                 Profile
	rate                    float64
	seed, sequence          uint32
	params                  Params
	current, old            strike
	fadeLeft                int
	chokeLeft               int
	fadeFrames, chokeFrames int
}

func NewVoice(profile Profile, sampleRate int, seed uint32) (*Voice, error) {
	if profile >= ProfileCount {
		return nil, Error("modeled kit profile is out of range")
	}
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("modeled kit sample rate must be 44100, 48000, or 96000")
	}
	v := &Voice{profile: profile, rate: float64(sampleRate), seed: seed, params: DefaultParams(), fadeFrames: max(1, sampleRate/1000), chokeFrames: max(1, sampleRate/200)}
	return v, nil
}

func (v *Voice) SetParams(params Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	v.params = params
	return nil
}

func (v *Voice) Params() Params { return v.params }

// Reset clears every tail and restores the four-strike variation sequence.
func (v *Voice) Reset() {
	v.current, v.old = strike{}, strike{}
	v.sequence, v.fadeLeft, v.chokeLeft = 0, 0, 0
}

func (v *Voice) Active() bool { return v.current.active || v.fadeLeft > 0 }

// Choke damps all ringing state in 5 ms. Repeated choke commands cannot extend
// a fade already in progress. Hat-family coordination belongs to the kit mixer.
func (v *Voice) Choke() {
	if v.Active() && v.chokeLeft == 0 {
		v.chokeLeft = v.chokeFrames
	}
}

func random(seed *uint32) float64 {
	x := *seed
	if x == 0 {
		x = 0x6d2b79f5
	}
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*seed = x
	return float64(int32(x)) / 2147483648
}

// Hit excites a finite strike. Velocity zero leaves the current tail untouched.
// Four virtual contact variations are deterministic; Reset reproduces them.
func (v *Voice) Hit(velocity uint8) {
	if velocity == 0 {
		return
	}
	if v.current.active {
		v.old = v.current
		// Preserve a choke already applied to the predecessor when retriggering.
		if v.chokeLeft > 0 {
			gain := float64(v.chokeLeft) / float64(v.chokeFrames)
			for i := 0; i < v.old.count; i++ {
				v.old.modes[i].x *= gain
				v.old.modes[i].y *= gain
			}
			v.old.noiseAmp *= gain
		}
		v.fadeLeft = v.fadeFrames
	}
	v.chokeLeft = 0
	s := &v.current
	*s = strike{active: true}
	r := &recipes[v.profile]
	variation := v.sequence & 3
	v.sequence++
	s.noise = v.seed ^ uint32(v.profile+1)*0x9e3779b9 ^ (variation+1)*0x85ebca6b
	jitter := [4]float64{-.75, .25, .75, -.25}[variation]
	position := min(1, max(0, v.params.Position+.018*jitter+v.params.Humanize*jitter))
	vel := float64(min(velocity, 127)) / 127
	strength := vel * math.Sqrt(vel) * (1 + .25*v.params.Humanize*jitter)
	hardness := vel * vel
	level := r.level * strength
	tune := v.params.Tune * (1 + .015*v.params.Humanize*jitter)
	s.contact = max(2, int(r.contact*(1.7-1.1*hardness)*(1+.055*jitter)*v.rate))
	s.remaining = int(max(r.t60, r.noiseT60)*v.params.Decay*3*v.rate) + s.contact
	s.noiseAmp = r.noise * strength * (.28 + .72*hardness)
	s.noisePole = math.Exp(-math.Log(1000) / (r.noiseT60 * v.params.Decay * v.rate))
	s.filter.configure(r.low*(.72+.4*hardness), r.high*(.55+.45*hardness), v.rate)
	s.followerA = 1 - math.Exp(-1/(.0008*v.rate))
	s.bendAlpha = 1 - math.Exp(-1/(.024*v.rate))
	if v.profile == Snare || v.profile == Rimshot {
		// The wire contact is driven by both its own strike and membrane
		// displacement. Soft ghost notes excite much less wire energy.
		s.bodyCoupling = .7 * vel * vel
	}
	ratios, weights := &membraneRatios, &membraneWeights
	metal := v.profile >= HatClosed
	if metal {
		ratios, weights = &plateRatios, &plateWeights
	} else if v.profile == Rimshot {
		ratios, weights = &rimRatios, &rimWeights
	} else if v.profile == CrossStick {
		ratios, weights = &stickRatios, &stickWeights
	}
	weightSum := 0.0
	for i := 0; i < r.count; i++ {
		if r.frequency*tune*ratios[i]*(1+r.bend*hardness) < .45*v.rate {
			weightSum += weights[i]
		}
	}
	for i := 0; i < r.count; i++ {
		frequency := r.frequency * tune * ratios[i]
		if v.profile == Kick && i >= 5 {
			// Shell and beater-head resonances above the membrane bank.
			frequency *= 1.45
		}
		startFrequency := frequency * (1 + r.bend*hardness)
		if startFrequency >= .45*v.rate {
			continue
		}
		m := &s.modes[s.count]
		decay := r.t60 * v.params.Decay / (1 + .24*float64(i))
		if metal {
			decay = r.t60 * v.params.Decay / (1 + .065*float64(i))
		}
		radius := math.Exp(-math.Log(1000) / (decay * v.rate))
		m.s, m.c = math.Sincos(2 * math.Pi * startFrequency / v.rate)
		m.s, m.c = m.s*radius, m.c*radius
		m.targetS, m.targetC = math.Sincos(2 * math.Pi * frequency / v.rate)
		m.targetS, m.targetC = m.targetS*radius, m.targetC*radius
		brightness := 1.0
		if i > 0 {
			brightness = .18 + .82*hardness
		}
		// An approximate radial mode shape makes edge strikes excite more
		// upper membrane modes and moves nodal cancellations across modes.
		shape := 1.0
		if i > 0 {
			shape = (.45 + .85*position) * (.7 + .3*math.Cos(float64(i+1)*position*math.Pi))
		} else {
			shape = 1 - .35*position
		}
		contactVariation := 1 + .065*math.Sin(float64((i+1)*int(variation+1))*1.7)
		gain := level * weights[i] / weightSum * brightness * shape * contactVariation
		if metal {
			// Bell strikes concentrate low plate modes; bow/edge strikes
			// excite a wider wash. Dense modes alone cannot reproduce a real
			// cymbal's nonlinear travelling waves, so filtered noise supplies
			// an explicitly approximate collision wash.
			gain *= 1.8
			if v.profile == RideBell {
				gain *= 1.9 / (1 + .25*float64(i))
			}
			phase := .35 * random(&s.noise)
			m.y, m.x = math.Sincos(phase)
			m.x, m.y = m.x*gain, m.y*gain
		} else {
			m.x = gain
		}
		s.count++
	}
}

func (v *Voice) nextStrike(s *strike) float64 {
	if !s.active {
		return 0
	}
	body, energy := 0.0, 0.0
	for i := 0; i < s.count; i++ {
		m := &s.modes[i]
		if m.x == 0 && m.y == 0 {
			continue
		}
		if recipes[v.profile].bend != 0 {
			// Convex pole interpolation follows the relaxing membrane
			// tension without trigonometry or unstable rotating coefficients.
			m.c += s.bendAlpha * (m.targetC - m.c)
			m.s += s.bendAlpha * (m.targetS - m.s)
		}
		x := m.c*m.x - m.s*m.y
		m.y = m.s*m.x + m.c*m.y
		m.x = x
		if math.Abs(m.x)+math.Abs(m.y) < 1e-9 {
			m.x, m.y = 0, 0
			continue
		}
		body += m.y
		energy += math.Abs(m.x) + math.Abs(m.y)
	}
	s.wireFollower += s.followerA * (math.Abs(body) - s.wireFollower)
	noiseLevel := s.noiseAmp + s.bodyCoupling*s.wireFollower
	noise := 0.0
	if noiseLevel > 1e-9 {
		noise = s.filter.next(random(&s.noise) * noiseLevel)
		s.noiseAmp *= s.noisePole
	} else {
		// Flush the short filter tail before retiring a finished strike.
		noise = s.filter.next(0)
	}
	attack := min(1, float64(s.age)/float64(s.contact))
	s.age++
	s.remaining--
	if s.remaining <= 0 || energy+noiseLevel+math.Abs(noise) < 1e-9 && s.age > s.contact {
		s.active = false
	}
	return (body + noise) * attack
}

func (v *Voice) Next() float32 {
	output := v.nextStrike(&v.current)
	if v.fadeLeft > 0 {
		output += v.nextStrike(&v.old) * float64(v.fadeLeft) / float64(v.fadeFrames)
		v.fadeLeft--
		if v.fadeLeft == 0 {
			v.old.active = false
		}
	}
	if v.chokeLeft > 0 {
		output *= float64(v.chokeLeft) / float64(v.chokeFrames)
		v.chokeLeft--
		if v.chokeLeft == 0 {
			v.current.active, v.old.active, v.fadeLeft = false, false, 0
		}
	}
	return float32(output)
}
