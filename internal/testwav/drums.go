package testwav

import "math"

// Drum names a synthetic percussion voice for Take. The voices are noise
// bursts and decaying sines, so tests need no recorded audio.
type Drum string

const (
	Kick      Drum = "kick"
	Snare     Drum = "snare"
	ClosedHat Drum = "closed-hat"
	OpenHat   Drum = "open-hat"
	Crash     Drum = "crash"
	// Ride is a long ring that swells and fades about 2.5 times a second, as
	// a cymbal's close modes beat against each other.
	Ride Drum = "ride"
)

// drumLength is each voice's length in seconds. Take lets a hit ring for this
// long before it adds the quiet that precedes the next hit.
var drumLength = map[Drum]float64{Kick: .45, Snare: .35, ClosedHat: .12, OpenHat: 1.4, Crash: 2.2, Ride: 2.6}

// splitmix is a small deterministic generator. Take must not depend on the
// global random source, so every run produces the same bytes.
type splitmix uint64

func (s *splitmix) next() float64 {
	*s += 0x9e3779b97f4a7c15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	return float64(z>>11)/float64(1<<53)*2 - 1
}

func drumSample(d Drum, t float64, noise float64) float64 {
	switch d {
	case Kick:
		phase := 2 * math.Pi * (55*t + 60*.02*(1-math.Exp(-t/.02)))
		return math.Sin(phase)*math.Exp(-t/.15) + .25*noise*math.Exp(-t/.004)
	case Snare:
		return .55*noise*math.Exp(-t/.07) + .45*math.Sin(2*math.Pi*190*t)*math.Exp(-t/.09)
	case ClosedHat:
		return noise * math.Exp(-t/.016)
	case OpenHat:
		return noise * math.Exp(-t/.28)
	case Ride:
		return noise * math.Exp(-t/1.1) * (1 + .85*math.Sin(2*math.Pi*2.5*t+1))
	default:
		return noise * math.Exp(-t/.7)
	}
}

// Take returns a mono PCM24 WAV with one hit of d per level, in order. Each
// level is that hit's peak amplitude (0..1). Hits start 750 ms apart, or the
// voice's length plus 300 ms when that is longer, after 200 ms of lead-in; a
// faint noise bed (about -84 dBFS) runs throughout, as in a real room. Hits of
// the same voice differ from each other, like round robins.
func Take(rate int, d Drum, levels ...float64) []byte {
	length := int(drumLength[d] * float64(rate))
	if length == 0 {
		panic("testwav: unknown drum " + string(d))
	}
	slot := max(length+rate*3/10, rate*3/4)
	lead := rate / 5
	frames := lead + slot*len(levels)
	pcm := make([]float64, frames)
	bed := splitmix(0x5eed)
	for i := range pcm {
		pcm[i] = bed.next() * 6e-5
	}
	seed := splitmix(len(d))
	for _, c := range []byte(d) {
		seed = seed*31 + splitmix(c)
	}
	at := lead
	for _, level := range levels {
		noise := seed
		seed += 0x1234567
		hit := make([]float64, length)
		peak := 0.0
		for i := range hit {
			hit[i] = drumSample(d, float64(i)/float64(rate), noise.next())
			peak = max(peak, math.Abs(hit[i]))
		}
		for i := range hit {
			pcm[at+i] += hit[i] / peak * level
		}
		at += slot
	}
	data := Bytes(rate, 1, 24, frames, 1)
	for i, x := range pcm {
		v := int32(math.Round(max(-1, min(1, x)) * 8388607))
		data[44+3*i] = byte(v)
		data[44+3*i+1] = byte(v >> 8)
		data[44+3*i+2] = byte(v >> 16)
	}
	return data
}
