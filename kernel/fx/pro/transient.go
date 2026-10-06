package pro

import "math"

type TransientParams struct {
	Enabled   bool
	AttackDB  float64 // -18..18, transient gain relative to sustained material
	SustainDB float64 // -18..18, sustained material gain
	FastMs    float64 // .1..10, fast envelope attack
	SlowMs    float64 // 10..100, reference envelope attack
	ReleaseMs float64 // 20..1000, shared envelope release
}

func DefaultTransientParams() TransientParams {
	return TransientParams{FastMs: 1, SlowMs: 30, ReleaseMs: 120}
}

// Transient uses linked fast/slow amplitude envelopes. Their normalized
// difference selects attack versus sustain gain; a smoothed linear gain avoids
// discontinuities. A 1 ms lookahead preserves the leading sample of an impact.
// There is no clipping or saturation stage.
type Transient struct {
	fast, slow, gain                   float64
	fastAttack, slowAttack, release    float64
	attackGain, sustainGain, smoothing float64
	bypass                             bool
	audio                              [2][]float32
	write, delay                       int
}

func NewTransient(sampleRate int, p TransientParams) (*Transient, error) {
	if !validRate(sampleRate) {
		return nil, ErrSampleRate
	}
	if !finite(p.AttackDB) || !finite(p.SustainDB) || !finite(p.FastMs) || !finite(p.SlowMs) || !finite(p.ReleaseMs) ||
		p.AttackDB < -18 || p.AttackDB > 18 || p.SustainDB < -18 || p.SustainDB > 18 || p.FastMs < .1 || p.FastMs > 10 ||
		p.SlowMs < 10 || p.SlowMs > 100 || p.SlowMs <= p.FastMs || p.ReleaseMs < 20 || p.ReleaseMs > 1000 {
		return nil, ErrParams
	}
	rate := float64(sampleRate)
	t := &Transient{
		fastAttack:  1 - math.Exp(-1/(p.FastMs*.001*rate)),
		slowAttack:  1 - math.Exp(-1/(p.SlowMs*.001*rate)),
		release:     1 - math.Exp(-1/(p.ReleaseMs*.001*rate)),
		attackGain:  dbGain(p.AttackDB),
		sustainGain: dbGain(p.SustainDB),
		smoothing:   1 - math.Exp(-1/(.0005*rate)),
		bypass:      p.AttackDB == 0 && p.SustainDB == 0,
	}
	if !t.bypass {
		t.delay = int(math.Ceil(.001 * rate))
		for channel := range t.audio {
			t.audio[channel] = make([]float32, t.delay+1)
		}
	}
	t.Reset()
	return t, nil
}

func (t *Transient) Process(left, right float32) (float32, float32) {
	if t.bypass {
		return left, right
	}
	level := math.Max(math.Abs(float64(left)), math.Abs(float64(right)))
	fastCoeff, slowCoeff := t.release, t.release
	if level > t.fast {
		fastCoeff = t.fastAttack
	}
	if level > t.slow {
		slowCoeff = t.slowAttack
	}
	t.fast = clean(t.fast + (level-t.fast)*fastCoeff)
	t.slow = clean(t.slow + (level-t.slow)*slowCoeff)
	attack := 0.0
	if t.fast > 1e-12 {
		attack = math.Max(0, math.Min(1, (t.fast-t.slow)/t.fast))
	}
	target := t.sustainGain + attack*(t.attackGain-t.sustainGain)
	t.gain += (target - t.gain) * t.smoothing
	t.audio[0][t.write], t.audio[1][t.write] = left, right
	read := t.write + 1
	if read == len(t.audio[0]) {
		read = 0
	}
	l, r := float32(float64(t.audio[0][read])*t.gain), float32(float64(t.audio[1][read])*t.gain)
	t.write = read
	return l, r
}

func (t *Transient) Reset() {
	t.fast, t.slow, t.gain = 0, 0, t.sustainGain
	for channel := range t.audio {
		clear(t.audio[channel])
	}
	t.write = 0
}

func (t *Transient) LatencyFrames() int { return t.delay }
