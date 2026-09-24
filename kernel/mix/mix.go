// Package mix provides the M0 dry music bus and linked master limiter.
package mix

import "math"

const MusicGainDB = -3.0
const CeilingDB = -.3

type Track struct{ Left, Right float32 }

func NewTrack(gainDB, pan float64, mute bool) Track {
	if mute {
		return Track{}
	}
	angle := (pan + 1) * math.Pi / 4
	gain := math.Pow(10, gainDB/20)
	return Track{Left: float32(gain * math.Cos(angle)), Right: float32(gain * math.Sin(angle))}
}

type Dry struct{ Left, Right float32 }

func (d *Dry) Add(left, right float32, track Track) {
	d.Left += left * track.Left
	d.Right += right * track.Right
}

func (d Dry) Music() (float32, float32) {
	const musicGain = .7079457843841379 // -3 dB
	return d.Left * musicGain, d.Right * musicGain
}

type stereo struct{ left, right float32 }
type peakItem struct {
	index int64
	peak  float64
}

// Limiter delays the signal by 1.5 ms, then applies one gain to both channels.
// The monotonic peak queue finds the largest sample in the lookahead window
// without scanning the window per sample.
type Limiter struct {
	lookahead                   int
	ring                        [145]stereo
	peaks                       [145]peakItem
	head, length                int
	position                    int64
	gain, releaseAlpha, ceiling float64
	fault                       bool
}

func NewLimiter(sampleRate int) (*Limiter, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("unsupported limiter sample rate")
	}
	lookahead := int(math.Round(float64(sampleRate) * .0015))
	return &Limiter{lookahead: lookahead, gain: 1, releaseAlpha: 1 - math.Exp(-1/(.05*float64(sampleRate))), ceiling: math.Pow(10, CeilingDB/20)}, nil
}

type Error string

func (e Error) Error() string { return string(e) }

func (l *Limiter) LatencyFrames() int { return l.lookahead }
func (l *Limiter) Ceiling() float64   { return l.ceiling }
func (l *Limiter) Fault() bool        { return l.fault }

func (l *Limiter) Process(left, right float32) (float32, float32, bool) {
	if l.fault {
		return 0, 0, false
	}
	if math.IsNaN(float64(left)) || math.IsNaN(float64(right)) || math.IsInf(float64(left), 0) || math.IsInf(float64(right), 0) {
		l.fault = true
		return 0, 0, false
	}
	n := l.position
	capacity := l.lookahead + 1
	l.ring[n%int64(capacity)] = stereo{left, right}
	cutoff := n - int64(l.lookahead)
	for l.length > 0 && l.peaks[l.head].index < cutoff {
		l.head = (l.head + 1) % capacity
		l.length--
	}
	peak := math.Max(math.Abs(float64(left)), math.Abs(float64(right)))
	for l.length > 0 {
		last := (l.head + l.length - 1) % capacity
		if l.peaks[last].peak > peak {
			break
		}
		l.length--
	}
	l.peaks[(l.head+l.length)%capacity] = peakItem{index: n, peak: peak}
	l.length++
	l.position++
	if n < int64(l.lookahead) {
		return 0, 0, false
	}
	target := 1.0
	if l.peaks[l.head].peak > l.ceiling {
		target = l.ceiling / l.peaks[l.head].peak
	}
	if target < l.gain {
		l.gain = target
	} else {
		l.gain += (target - l.gain) * l.releaseAlpha
	}
	delayed := l.ring[(n-int64(l.lookahead))%int64(capacity)]
	outL := float64(delayed.left) * l.gain
	outR := float64(delayed.right) * l.gain
	// Guard the final float32 conversion at the exact ceiling.
	outL = math.Max(-l.ceiling, math.Min(l.ceiling, outL))
	outR = math.Max(-l.ceiling, math.Min(l.ceiling, outR))
	return float32(outL), float32(outR), true
}
