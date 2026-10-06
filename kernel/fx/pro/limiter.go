package pro

import "math"

const (
	limiterTaps   = 64
	limiterPhases = 8
	limiterHalf   = limiterTaps / 2
	// The detector has finite interpolation and finite phase spacing. Leave
	// 0.8 dB below the requested ceiling rather than claiming exact brickwall
	// reconstruction from an eight-phase estimate.
	limiterMarginDB = .8
)

type LimiterParams struct {
	CeilingDBTP float64 // -12..0
	LookaheadMs float64 // 1..10; total latency adds 64 interpolation frames
	ReleaseMs   float64 // 20..1000
}

func DefaultLimiterParams() LimiterParams {
	return LimiterParams{CeilingDBTP: -1, LookaheadMs: 3, ReleaseMs: 100}
}

type peakEntry struct {
	frame int64
	value float64
}

// Limiter is a linked stereo lookahead limiter with an eight-phase windowed
// sinc detector. It smooths gain instead of clipping samples. The 0.8 dB safety
// margin covers the tested reconstruction fixtures; it is not an analytic
// guarantee for every possible out-of-band or adversarial input. Hosts should
// measure the delivered programme, including boundaries, after processing.
type Limiter struct {
	coeff                 [limiterTaps][limiterPhases - 1]float64
	ring                  [2][2 * limiterTaps]float64
	audio                 [2][]float32
	queue                 []peakEntry
	queueHead, queueCount int
	write                 int
	frames                int64
	delay, window         int
	ceiling               float64
	attack, release       float64
	gain                  float64
	fault                 bool
}

func NewLimiter(sampleRate int, p LimiterParams) (*Limiter, error) {
	if !validRate(sampleRate) {
		return nil, ErrSampleRate
	}
	if !finite(p.CeilingDBTP) || !finite(p.LookaheadMs) || !finite(p.ReleaseMs) ||
		p.CeilingDBTP < -12 || p.CeilingDBTP > 0 || p.LookaheadMs < 1 || p.LookaheadMs > 10 || p.ReleaseMs < 20 || p.ReleaseMs > 1000 {
		return nil, ErrParams
	}
	lookahead := int(math.Ceil(p.LookaheadMs * float64(sampleRate) / 1000))
	l := &Limiter{
		delay:   lookahead + 2*limiterHalf,
		window:  lookahead + 2*limiterHalf,
		ceiling: dbGain(p.CeilingDBTP - limiterMarginDB),
		// Five attack time constants fit inside the lookahead. The margin
		// also covers the finite exponential approach to the target gain.
		attack:  1 - math.Exp(-5/float64(lookahead)),
		release: 1 - math.Exp(-1/(p.ReleaseMs*.001*float64(sampleRate))),
		gain:    1,
		queue:   make([]peakEntry, lookahead+2*limiterHalf+1),
	}
	for channel := range l.audio {
		l.audio[channel] = make([]float32, l.delay+1)
	}
	for phase := 0; phase < limiterPhases-1; phase++ {
		fraction := float64(phase+1) / limiterPhases
		sum := 0.0
		for tap := 0; tap < limiterTaps; tap++ {
			x := float64(tap-(limiterHalf-1)) - fraction
			sinc := 1.0
			if x != 0 {
				sinc = math.Sin(math.Pi*x) / (math.Pi * x)
			}
			position := float64(tap) / (limiterTaps - 1)
			window := .42 - .5*math.Cos(2*math.Pi*position) + .08*math.Cos(4*math.Pi*position)
			l.coeff[tap][phase] = sinc * window
			sum += sinc * window
		}
		for tap := 0; tap < limiterTaps; tap++ {
			l.coeff[tap][phase] /= sum
		}
	}
	return l, nil
}

func (l *Limiter) detector(left, right float64) float64 {
	index := int(l.frames & (limiterTaps - 1))
	l.ring[0][index], l.ring[1][index] = left, right
	// Mirror the ring so every FIR window is contiguous. The seven phases
	// reuse each stereo input load rather than wrapping/indexing it seven times.
	l.ring[0][index+limiterTaps], l.ring[1][index+limiterTaps] = left, right
	center := int((l.frames - limiterHalf) & (limiterTaps - 1))
	peak := math.Max(math.Abs(l.ring[0][center]), math.Abs(l.ring[1][center]))
	start := (index + 1) & (limiterTaps - 1)
	var a0, a1, a2, a3, a4, a5, a6 float64
	var b0, b1, b2, b3, b4, b5, b6 float64
	for tap := 0; tap < limiterTaps; tap++ {
		a, b := l.ring[0][start+tap], l.ring[1][start+tap]
		h := &l.coeff[tap]
		a0 += h[0] * a
		a1 += h[1] * a
		a2 += h[2] * a
		a3 += h[3] * a
		a4 += h[4] * a
		a5 += h[5] * a
		a6 += h[6] * a
		b0 += h[0] * b
		b1 += h[1] * b
		b2 += h[2] * b
		b3 += h[3] * b
		b4 += h[4] * b
		b5 += h[5] * b
		b6 += h[6] * b
	}
	for _, value := range [14]float64{a0, a1, a2, a3, a4, a5, a6, b0, b1, b2, b3, b4, b5, b6} {
		peak = math.Max(peak, math.Abs(value))
	}
	return peak
}

func (l *Limiter) pushPeak(value float64) float64 {
	frame := l.frames - limiterHalf
	oldest := frame - int64(l.window) + 1
	for l.queueCount > 0 && l.queue[l.queueHead].frame < oldest {
		l.queueHead = (l.queueHead + 1) % len(l.queue)
		l.queueCount--
	}
	for l.queueCount > 0 {
		last := (l.queueHead + l.queueCount - 1) % len(l.queue)
		if l.queue[last].value > value {
			break
		}
		l.queueCount--
	}
	last := (l.queueHead + l.queueCount) % len(l.queue)
	l.queue[last] = peakEntry{frame: frame, value: value}
	l.queueCount++
	return l.queue[l.queueHead].value
}

func (l *Limiter) Process(left, right float32) (float32, float32) {
	if l.fault || !finite(float64(left)) || !finite(float64(right)) {
		l.fault = true
		return 0, 0
	}
	peak := l.pushPeak(l.detector(float64(left), float64(right)))
	target := 1.0
	if peak > l.ceiling {
		target = l.ceiling / peak
	}
	coefficient := l.release
	if target < l.gain {
		coefficient = l.attack
	}
	l.gain += (target - l.gain) * coefficient
	l.audio[0][l.write], l.audio[1][l.write] = left, right
	read := l.write + 1
	if read == len(l.audio[0]) {
		read = 0
	}
	outL, outR := float32(float64(l.audio[0][read])*l.gain), float32(float64(l.audio[1][read])*l.gain)
	l.write = read
	l.frames++
	return outL, outR
}

func (l *Limiter) Reset() {
	l.ring = [2][2 * limiterTaps]float64{}
	for channel := range l.audio {
		clear(l.audio[channel])
	}
	clear(l.queue)
	l.queueHead, l.queueCount, l.write, l.frames = 0, 0, 0, 0
	l.gain, l.fault = 1, false
}

func (l *Limiter) LatencyFrames() int       { return l.delay }
func (l *Limiter) Fault() bool              { return l.fault }
func (l *Limiter) GainReductionDB() float64 { return -20 * math.Log10(l.gain) }
