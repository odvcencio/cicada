package pro

import (
	"math"

	"m31labs.dev/cicada/kernel/fx"
)

type ReverbParams struct {
	Enabled  bool
	Tail     fx.ReverbParams // Mix is controlled by the enclosing Mix field
	EarlyMix float64         // 0..1, blend from FDN tail to early reflections
	Width    float64         // 0..2, wet side gain
	Mix      float64         // 0..1, dry/wet blend
}

func DefaultReverbParams() ReverbParams {
	p := fx.DefaultReverbParams()
	return ReverbParams{Tail: p, EarlyMix: .2, Width: 1, Mix: .2}
}

// Reverb adds an explicit stereo early-reflection field and wet-only width to
// the existing modulated eight-line FDN. Reflections scale with room size and
// predelay, and feed a separate broadband-decaying tail. Mix zero is exact dry.
type Reverb struct {
	tail               *fx.Reverb
	buffer             [2][]float64
	index              int
	taps               [2][6]int
	mix                float64
	earlyMix           float64
	width              float64
	fault              bool
	quiet, quietFrames int
	dormant            bool
}

var reflectionTimes = [2][6]float64{
	{4.3, 8.7, 14.9, 21.3, 31.7, 43.1},
	{5.9, 10.1, 16.7, 24.1, 34.9, 47.3},
}

var reflectionGains = [6]float64{.34, -.23, .18, .13, -.09, .06}

func NewReverb(sampleRate int, p ReverbParams) (*Reverb, error) {
	if !validRate(sampleRate) {
		return nil, ErrSampleRate
	}
	if !finite(p.EarlyMix) || !finite(p.Width) || !finite(p.Mix) || p.EarlyMix < 0 || p.EarlyMix > 1 ||
		p.Width < 0 || p.Width > 2 || p.Mix < 0 || p.Mix > 1 {
		return nil, ErrParams
	}
	p.Tail.Mix = 1
	if err := p.Tail.Validate(); err != nil {
		return nil, err
	}
	tail, err := fx.NewReverb(sampleRate)
	if err != nil {
		return nil, err
	}
	if err = tail.SetParams(p.Tail); err != nil {
		return nil, err
	}
	tail.Reset()
	r := &Reverb{tail: tail, mix: p.Mix, earlyMix: p.EarlyMix, width: p.Width, quietFrames: sampleRate / 2, dormant: true}
	maxTap := 0
	for channel := range r.taps {
		for i, ms := range reflectionTimes[channel] {
			frames := int(math.Round((ms*p.Tail.Size + p.Tail.PredelayMs) * float64(sampleRate) / 1000))
			r.taps[channel][i] = frames
			maxTap = max(maxTap, frames)
		}
	}
	for channel := range r.buffer {
		r.buffer[channel] = make([]float64, maxTap+1)
	}
	return r, nil
}

func (r *Reverb) Process(left, right float32) (float32, float32) {
	if r.fault || !finite(float64(left)) || !finite(float64(right)) {
		r.fault = true
		return 0, 0
	}
	if r.mix == 0 {
		return left, right
	}
	if r.dormant {
		if left == 0 && right == 0 {
			return 0, 0
		}
		r.dormant = false
	}
	l, rr := r.tail.Process(left, right)
	r.fault = r.tail.Fault()
	r.buffer[0][r.index], r.buffer[1][r.index] = float64(left), float64(right)
	var early [2]float64
	for channel := range early {
		for i, frames := range r.taps[channel] {
			index := r.index - frames
			if index < 0 {
				index += len(r.buffer[channel])
			}
			// Alternate reflections cross the room rather than duplicating mono.
			source := channel ^ (i & 1)
			early[channel] += r.buffer[source][index] * reflectionGains[i]
		}
	}
	r.index++
	if r.index == len(r.buffer[0]) {
		r.index = 0
	}
	wetL := float64(l)*(1-r.earlyMix) + early[0]*r.earlyMix
	wetR := float64(rr)*(1-r.earlyMix) + early[1]*r.earlyMix
	// Once the entire observable tail stays below -400 dBFS for half a
	// second, retire the FDN state before it can drift into denormals. The
	// interval exceeds the largest allowed predelay/reflection gap.
	if left == 0 && right == 0 && math.Abs(wetL)+math.Abs(wetR) < 1e-20 {
		r.quiet++
		if r.quiet >= r.quietFrames {
			r.Reset()
			return 0, 0
		}
	} else {
		r.quiet = 0
	}
	mid, side := (wetL+wetR)*.5, (wetL-wetR)*.5*r.width
	return float32(float64(left)*(1-r.mix) + (mid+side)*r.mix),
		float32(float64(right)*(1-r.mix) + (mid-side)*r.mix)
}

func (r *Reverb) Reset() {
	r.tail.Reset()
	for channel := range r.buffer {
		clear(r.buffer[channel])
	}
	r.index, r.quiet, r.fault, r.dormant = 0, 0, false, true
}

func (r *Reverb) Fault() bool        { return r.fault }
func (r *Reverb) LatencyFrames() int { return 0 }
