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
	// LongCrash is a crash that swells over about 12 ms, as a struck cymbal
	// does, and rings for 14 seconds: a decay of 2 seconds, 61 dB down by the
	// end. Placed with Spaced, its tail fills most of a take.
	LongCrash Drum = "long-crash"
	// BeatingTom is a tom whose ring swells and fades 12 times a second: a
	// second mode, nearly as loud as the first, sits 12 Hz above it. Each fade
	// is a deep null, and the energy climbs out of it within a few milliseconds.
	BeatingTom Drum = "beating-tom"
)

// drumLength is each voice's length in seconds. Take lets a hit ring for this
// long before it adds the quiet that precedes the next hit.
var drumLength = map[Drum]float64{Kick: .45, Snare: .35, ClosedHat: .12, OpenHat: 1.4, Crash: 2.2, Ride: 2.6, LongCrash: 14, BeatingTom: 1.7}

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
	case LongCrash:
		return noise * (1 - math.Exp(-t/.012)) * math.Exp(-t/2)
	case BeatingTom:
		return math.Exp(-t/.7)*(math.Sin(2*math.Pi*82*t)+.9*math.Sin(2*math.Pi*94*t+.7)) + .3*noise*math.Exp(-t/.004)
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
	seed := firstSeed(d)
	at := lead
	for _, level := range levels {
		noise := seed
		seed += 0x1234567
		hit, peak := voice(d, rate, length, noise)
		for i := range hit {
			pcm[at+i] += hit[i] / peak * level
		}
		at += slot
	}
	return pcm24(rate, pcm)
}

// Layout places the hits of Spaced. All times are in seconds.
type Layout struct {
	// Lead is the room sound before the first hit.
	Lead float64
	// Spacing runs from the start of one hit to the start of the next. A hit
	// that rings longer goes on under the hit that follows.
	Spacing float64
	// After is the recording that remains once the last hit has started.
	After float64
	// RoomDB is the room's noise level in dBFS (RMS). Zero keeps the faint bed
	// that Take uses.
	RoomDB float64
}

// Spaced returns a mono PCM24 WAV with one hit of d per level, placed by
// layout. Unlike Take it lets hits overlap, so a cymbal's tail runs on under
// the next hit and can fill most of the take. A level above 1 clips, as a
// recording made with the input gain too high does.
func Spaced(rate int, d Drum, layout Layout, levels ...float64) []byte {
	length := int(drumLength[d] * float64(rate))
	if length == 0 {
		panic("testwav: unknown drum " + string(d))
	}
	frames := int((layout.Lead + layout.Spacing*float64(max(0, len(levels)-1)) + layout.After) * float64(rate))
	pcm := make([]float64, frames)
	room := 6e-5
	if layout.RoomDB != 0 {
		room = math.Pow(10, layout.RoomDB/20) * math.Sqrt(3)
	}
	bed := splitmix(0x5eed)
	for i := range pcm {
		pcm[i] = bed.next() * room
	}
	seed := firstSeed(d)
	for k, level := range levels {
		noise := seed
		seed += 0x1234567
		hit, peak := voice(d, rate, length, noise)
		at := int((layout.Lead + layout.Spacing*float64(k)) * float64(rate))
		for i := range hit {
			if at+i < frames {
				pcm[at+i] += hit[i] / peak * level
			}
		}
	}
	return pcm24(rate, pcm)
}

// firstSeed gives each voice its own noise, so voices differ from each other.
func firstSeed(d Drum) splitmix {
	seed := splitmix(len(d))
	for _, c := range []byte(d) {
		seed = seed*31 + splitmix(c)
	}
	return seed
}

// voice synthesizes one hit of length frames and returns it with its peak.
func voice(d Drum, rate, length int, noise splitmix) ([]float64, float64) {
	hit := make([]float64, length)
	peak := 0.0
	for i := range hit {
		hit[i] = drumSample(d, float64(i)/float64(rate), noise.next())
		peak = max(peak, math.Abs(hit[i]))
	}
	return hit, peak
}

// pcm24 encodes mono samples as a PCM24 WAV, clipping at full scale.
func pcm24(rate int, pcm []float64) []byte {
	data := Bytes(rate, 1, 24, len(pcm), 1)
	for i, x := range pcm {
		v := int32(math.Round(max(-1, min(1, x)) * 8388607))
		data[44+3*i] = byte(v)
		data[44+3*i+1] = byte(v >> 8)
		data[44+3*i+2] = byte(v >> 16)
	}
	return data
}
