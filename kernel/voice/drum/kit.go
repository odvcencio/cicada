// Package drum implements Cicada's six M0 synthesized drum lanes. Each lane
// owns one bounded voice and one deterministic noise stream.
package drum

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
)

type Lane uint8

const (
	BD Lane = iota
	SD
	CH
	OH
	CP
	RS
	LaneCount
)

var Names = [LaneCount]string{"bd", "sd", "ch", "oh", "cp", "rs"}

// The six synthesis recipes have different native amplitudes. These fixed
// trims put a full-velocity hit with default params in the same pre-master
// peak range at each supported sample rate, before authored lane level/pan.
var nominalTrim = [LaneCount]float64{1.6, 1.7, 4.9, 4.9, 5.7, 1.5}

type Error string

func (e Error) Error() string { return string(e) }

type Params struct {
	Tune, Decay, Sweep, SweepTime, Click, Drive float64
	Tone, Mix, Snappy, Spread                   float64
	Metal                                       bool
	LevelDB, Pan                                float64
}

func DefaultParams(lane Lane) Params {
	p := Params{LevelDB: -6, Pan: 0}
	switch lane {
	case BD:
		p.Tune, p.Decay, p.Sweep, p.SweepTime, p.Click, p.Drive = 55, .4, 4, .03, .3, .2
	case SD:
		p.Tune, p.Tone, p.Mix, p.Snappy, p.Decay = 1, 1, .6, .18, .09
	case CH:
		p.Tune, p.Tone, p.Decay = 1, 7500, .06
	case OH:
		p.Tune, p.Tone, p.Decay = 1, 7500, .4
	case CP:
		p.Tone, p.Decay, p.Spread = 1100, .12, .01
	case RS:
		p.Tune, p.Decay = 1, .012
	}
	return p
}

func (p Params) Validate(lane Lane) error {
	if lane >= LaneCount {
		return Error("unsupported drum lane")
	}
	for _, value := range [...]float64{p.Tune, p.Decay, p.Sweep, p.SweepTime, p.Click, p.Drive, p.Tone, p.Mix, p.Snappy, p.Spread, p.LevelDB, p.Pan} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Error("drum parameter must be finite")
		}
	}
	if (p.LevelDB != -1000 && p.LevelDB < -60) || p.LevelDB > 6 || p.Pan < -1 || p.Pan > 1 {
		return Error("drum level or pan is out of range")
	}
	switch lane {
	case BD:
		if p.Tune < 40 || p.Tune > 120 || p.Decay < .08 || p.Decay > 1.5 || p.Sweep < 1 || p.Sweep > 8 || p.SweepTime < .01 || p.SweepTime > .08 || p.Click < 0 || p.Click > 1 || p.Drive < 0 || p.Drive > 1 {
			return Error("bass drum parameter is out of range")
		}
	case SD:
		if p.Tune < .7 || p.Tune > 1.4 || p.Tone < .5 || p.Tone > 2 || p.Mix < 0 || p.Mix > 1 || p.Snappy < .06 || p.Snappy > .4 || p.Decay < .04 || p.Decay > .3 {
			return Error("snare parameter is out of range")
		}
	case CH:
		if p.Tune < .8 || p.Tune > 1.25 || p.Tone < 5000 || p.Tone > 10000 || p.Decay < .02 || p.Decay > .15 {
			return Error("closed hat parameter is out of range")
		}
	case OH:
		if p.Tune < .8 || p.Tune > 1.25 || p.Tone < 5000 || p.Tone > 10000 || p.Decay < .15 || p.Decay > 1.2 {
			return Error("open hat parameter is out of range")
		}
	case CP:
		if p.Tone < 700 || p.Tone > 2000 || p.Decay < .06 || p.Decay > .4 || p.Spread < .006 || p.Spread > .016 {
			return Error("clap parameter is out of range")
		}
	case RS:
		if p.Tune < .7 || p.Tune > 1.4 || p.Decay < .004 || p.Decay > .05 {
			return Error("rimshot parameter is out of range")
		}
	}
	return nil
}

type filter struct {
	a1, a2, a3, k float64
	ic1, ic2      float64
}

func (f *filter) configure(cutoff, q, rate float64) {
	cutoff = min(cutoff, .45*rate)
	g := math.Tan(math.Pi * cutoff / rate)
	f.k = 1 / q
	f.a1 = 1 / (1 + g*(g+f.k))
	f.a2 = g * f.a1
	f.a3 = g * f.a2
	f.ic1, f.ic2 = 0, 0
}

func (f *filter) next(input float64) (band, high float64) {
	v3 := input - f.ic2
	v1 := f.a1*f.ic1 + f.a2*v3
	v2 := f.ic2 + f.a2*f.ic1 + f.a3*v3
	f.ic1 = 2*v1 - f.ic1
	f.ic2 = 2*v2 - f.ic2
	return v1, input - f.k*v1 - v2
}

type state struct {
	active                              bool
	age                                 int
	phase                               [6]float64
	amp, sweep, click                   float64
	velocity, noiseVelocity, accentGain float64
	noise                               uint32
	band, high1                         filter
	chokeRemaining                      int
}

type laneVoice struct {
	params            Params
	current, old      state
	fadeRemaining     int
	panL, panR, level float64
}

type Kit struct {
	rate  float64
	seed  uint32
	lanes [LaneCount]laneVoice
	fault bool
}

func New(sampleRate int, seed uint32) (*Kit, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("drum sample rate must be 44100, 48000, or 96000")
	}
	k := &Kit{rate: float64(sampleRate), seed: seed}
	for lane := Lane(0); lane < LaneCount; lane++ {
		if err := k.SetParams(lane, DefaultParams(lane)); err != nil {
			return nil, err
		}
		k.lanes[lane].current.noise = seed ^ uint32(lane+1)*0x9e3779b9
	}
	return k, nil
}

func (k *Kit) SetParams(lane Lane, params Params) error {
	if err := params.Validate(lane); err != nil {
		return err
	}
	v := &k.lanes[lane]
	v.params = params
	angle := (params.Pan + 1) * math.Pi / 4
	v.panL, v.panR = math.Cos(angle), math.Sin(angle)
	if params.LevelDB == -1000 {
		v.level = 0
	} else {
		v.level = math.Pow(10, params.LevelDB/20)
	}
	return nil
}

func (k *Kit) Params(lane Lane) Params { return k.lanes[lane].params }
func (k *Kit) Fault() bool             { return k.fault }

func (k *Kit) Reset() {
	k.fault = false
	for lane := Lane(0); lane < LaneCount; lane++ {
		v := &k.lanes[lane]
		v.current = state{noise: k.seed ^ uint32(lane+1)*0x9e3779b9}
		v.old = state{}
		v.fadeRemaining = 0
	}
}

func (k *Kit) Hit(lane Lane, velocity uint8, accent bool) {
	if lane >= LaneCount {
		return
	}
	if lane == CH {
		k.NoteOff(OH)
	}
	v := &k.lanes[lane]
	if v.current.active {
		v.old = v.current
		v.fadeRemaining = max(1, int(k.rate/1000))
	}
	noise := v.current.noise
	v.current = state{active: true, noise: noise, amp: 1, sweep: 1, click: 1}
	if accent {
		velocity = 127
		v.current.accentGain = math.Sqrt2
	} else {
		v.current.accentGain = 1
	}
	v.current.velocity = float64(velocity) / 127
	v.current.noiseVelocity = math.Pow(v.current.velocity, 1.5)
	v.current.band.configure(k.bandCutoff(lane), k.bandQ(lane), k.rate)
	v.current.high1.configure(8000, 0.707, k.rate)
}

func (k *Kit) NoteOff(lane Lane) {
	if lane >= LaneCount {
		return
	}
	v := &k.lanes[lane].current
	if v.active {
		v.chokeRemaining = max(1, int(k.rate*.005))
	}
}

func (k *Kit) NextStereo() (left, right float32) {
	if k.fault {
		return 0, 0
	}
	var l, r float64
	for lane := Lane(0); lane < LaneCount; lane++ {
		v := &k.lanes[lane]
		output := k.nextState(lane, &v.current, v.params)
		if v.fadeRemaining > 0 {
			old := k.nextState(lane, &v.old, v.params)
			output += old * float64(v.fadeRemaining) / max(1, k.rate/1000)
			v.fadeRemaining--
		}
		output *= nominalTrim[lane] * v.level
		l += output * v.panL
		r += output * v.panR
	}
	if math.IsNaN(l) || math.IsNaN(r) || math.IsInf(l, 0) || math.IsInf(r, 0) {
		k.fault = true
		return 0, 0
	}
	return float32(l), float32(r)
}

func (k *Kit) bandCutoff(lane Lane) float64 {
	p := k.lanes[lane].params
	switch lane {
	case SD:
		return 1800 * p.Tone
	case CH, OH:
		return p.Tone
	case CP:
		return p.Tone
	case RS:
		return 1700 * p.Tune
	}
	return 1000
}
func (k *Kit) bandQ(lane Lane) float64 {
	switch lane {
	case CH, OH:
		return 2
	case CP, SD:
		return 1.2
	}
	return 1
}

func (k *Kit) nextState(lane Lane, s *state, p Params) float64 {
	if !s.active {
		return 0
	}
	age := float64(s.age) / k.rate
	var output float64
	switch lane {
	case BD:
		frequency := p.Tune * (1 + p.Sweep*s.sweep)
		s.phase[0] = wrap(s.phase[0] + frequency/k.rate)
		body := math.Sin(2*math.Pi*s.phase[0]) * s.amp * s.velocity
		click := nextNoise(&s.noise) * s.click * p.Click * s.noiseVelocity
		output = fastmath.Tanh((body+click)*(1+p.Drive*3)) / (1 + p.Drive*.7)
		s.sweep *= math.Exp(-1 / (p.SweepTime * k.rate))
		s.amp *= math.Exp(-1 / (p.Decay * k.rate))
		s.click *= math.Exp(-1 / (.002 * k.rate))
	case SD:
		sweep := 1 + math.Exp(-age/.008)
		s.phase[0] = wrap(s.phase[0] + 180*p.Tune*sweep/k.rate)
		s.phase[1] = wrap(s.phase[1] + 330*p.Tune*sweep/k.rate)
		body := (math.Sin(2*math.Pi*s.phase[0])*math.Exp(-age/p.Decay) + .6*math.Sin(2*math.Pi*s.phase[1])*math.Exp(-age/.06)) * s.velocity
		band, _ := s.band.next(nextNoise(&s.noise))
		noise := band * math.Exp(-age/p.Snappy) * s.noiseVelocity
		output = fastmath.Tanh(body*(1-p.Mix) + noise*p.Mix*1.6)
	case CH, OH:
		bank := 0.0
		if p.Metal {
			s.phase[0] = wrap(s.phase[0] + 6000*p.Tune/k.rate)
			s.phase[1] = wrap(s.phase[1] + 8400*p.Tune/k.rate)
			bank = math.Sin(2*math.Pi*s.phase[0] + 8*math.Sin(2*math.Pi*s.phase[1]))
		} else {
			for i, frequency := range [...]float64{205.3, 304.4, 369.6, 522.7, 540, 800} {
				s.phase[i] = wrap(s.phase[i] + frequency*p.Tune/k.rate)
				if s.phase[i] < .5 {
					bank += 1
				} else {
					bank -= 1
				}
			}
			bank /= 6
		}
		band, _ := s.band.next(bank)
		_, high := s.high1.next(band)
		output = high * math.Exp(-age/p.Decay) * s.velocity
	case CP:
		band, _ := s.band.next(nextNoise(&s.noise))
		bursts := 0.0
		for i := 0; i < 4; i++ {
			delta := age - float64(i)*p.Spread
			if delta >= 0 {
				bursts += math.Exp(-delta / .006)
			}
		}
		tail := 0.0
		if age >= 3*p.Spread {
			tail = .3981071705534972 * math.Exp(-(age-3*p.Spread)/p.Decay)
		}
		output = band * (bursts + tail) * s.noiseVelocity * .4
	case RS:
		s.phase[0] = wrap(s.phase[0] + 1700*p.Tune/k.rate)
		s.phase[1] = wrap(s.phase[1] + 440*p.Tune/k.rate)
		triangle := 4*math.Abs(s.phase[0]-.5) - 1
		_, high := s.high1.next(nextNoise(&s.noise))
		body := (triangle*math.Exp(-age/.004) + math.Sin(2*math.Pi*s.phase[1])*math.Exp(-age/p.Decay)) * s.velocity
		noise := high * math.Exp(-age/.001) * s.noiseVelocity
		output = (body + noise) * .45
	}
	if s.chokeRemaining > 0 {
		output *= float64(s.chokeRemaining) / max(1, k.rate*.005)
		s.chokeRemaining--
		if s.chokeRemaining == 0 {
			s.active = false
		}
	}
	output *= s.accentGain
	s.age++
	if s.amp < 1e-6 && lane == BD || age > 4 || (lane != BD && age > 12*p.Decay) {
		s.active = false
	}
	return output
}

func nextNoise(seed *uint32) float64 {
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

func wrap(value float64) float64 {
	if value >= 1 {
		return value - 1
	}
	return value
}
