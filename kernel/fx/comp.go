package fx

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
)

type CompDetector uint8

const (
	PeakDetector CompDetector = iota
	RMSDetector
)

type CompParams struct {
	Detect     CompDetector
	Threshold  float64 // dBFS
	Ratio      float64
	Knee       float64 // dB
	AttackMs   float64
	ReleaseMs  float64
	MakeupAuto bool
	MakeupDB   float64
	Mix        float64
}

func DefaultCompParams() CompParams {
	return CompParams{Detect: PeakDetector, Threshold: -18, Ratio: 4, Knee: 6, AttackMs: 10, ReleaseMs: 100, MakeupAuto: true, Mix: 1}
}

func (p CompParams) Validate() error {
	if p.Detect > RMSDetector || !finite(p.Threshold) || !finite(p.Ratio) || !finite(p.Knee) || !finite(p.AttackMs) ||
		!finite(p.ReleaseMs) || !finite(p.MakeupDB) || !finite(p.Mix) || p.Threshold < -40 || p.Threshold > 0 ||
		p.Ratio < 1 || p.Ratio > 20 || p.Knee < 0 || p.Knee > 12 || p.AttackMs < .1 || p.AttackMs > 100 ||
		p.ReleaseMs < 10 || p.ReleaseMs > 1_000 || p.Mix < 0 || p.Mix > 1 || !p.MakeupAuto && (p.MakeupDB < -24 || p.MakeupDB > 24) {
		return Error("compressor parameter is invalid or out of range")
	}
	return nil
}

// Compressor is a linked stereo feed-forward compressor. External detector
// inputs use an 80 Hz sidechain highpass. All storage is fixed at construction.
type Compressor struct {
	params                CompParams
	sampleRate            float64
	rms                   []float64
	rmsIndex              int
	rmsSum                float64
	peak                  float64
	peakHold              int
	holdFrames            int
	sideLP                stereo
	sideAlpha             float64
	attack, release       float64
	kneeFloor             float64
	makeup, targetMakeup  float64
	mix, targetMix        float64
	smooth                float64
	reduction             float64
	lastLevel, lastTarget float64
	fault                 bool
}

func NewCompressor(sampleRate int) (*Compressor, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("unsupported compressor sample rate")
	}
	c := &Compressor{
		sampleRate: float64(sampleRate),
		rms:        make([]float64, sampleRate/100),
		holdFrames: int(math.Round(float64(sampleRate) / 1000)),
		sideAlpha:  1 - math.Exp(-2*math.Pi*80/float64(sampleRate)),
		smooth:     1 - math.Exp(-1/(.005*float64(sampleRate))),
	}
	if err := c.SetParams(DefaultCompParams()); err != nil {
		return nil, err
	}
	c.Reset()
	return c, nil
}

func (c *Compressor) Params() CompParams { return c.params }
func (c *Compressor) Fault() bool        { return c.fault }
func (c *Compressor) LatencyFrames() int { return 0 }

func (c *Compressor) SetParams(p CompParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	c.params = p
	c.attack = 1 - math.Exp(-1/(p.AttackMs*.001*c.sampleRate))
	c.release = 1 - math.Exp(-1/(p.ReleaseMs*.001*c.sampleRate))
	c.kneeFloor = math.Pow(10, (p.Threshold-p.Knee/2)/20)
	c.lastLevel = math.NaN()
	c.targetMakeup = p.MakeupDB
	if p.MakeupAuto {
		c.targetMakeup = -(p.Threshold * (1 - 1/p.Ratio)) / 2
	}
	c.targetMix = p.Mix
	return nil
}

func (c *Compressor) Reset() {
	clear(c.rms)
	c.rmsIndex, c.rmsSum, c.peak, c.peakHold = 0, 0, 0, 0
	c.sideLP = stereo{}
	c.makeup, c.mix, c.reduction, c.fault = c.targetMakeup, c.targetMix, 0, false
	c.lastLevel, c.lastTarget = math.NaN(), 0
}

func (c *Compressor) reductionDB(level float64) float64 {
	if level <= c.kneeFloor || c.params.Ratio == 1 {
		return 0
	}
	over := (20/(math.Log2E*math.Ln10))*fastmath.Log2(level) - c.params.Threshold
	slope := 1 - 1/c.params.Ratio
	knee := c.params.Knee
	if knee == 0 || over >= knee/2 {
		return math.Max(0, over) * slope
	}
	if over <= -knee/2 {
		return 0
	}
	x := over + knee/2
	return slope * x * x / (2 * knee)
}

func (c *Compressor) process(left, right, detectL, detectR float32, external bool) (float32, float32) {
	if c.fault {
		return 0, 0
	}
	l, r := float64(left), float64(right)
	dl, dr := float64(detectL), float64(detectR)
	if !finite(l) || !finite(r) || !finite(dl) || !finite(dr) {
		c.fault = true
		return 0, 0
	}
	if external {
		c.sideLP.left += (dl - c.sideLP.left) * c.sideAlpha
		c.sideLP.right += (dr - c.sideLP.right) * c.sideAlpha
		dl -= c.sideLP.left
		dr -= c.sideLP.right
	}
	var level float64
	if c.params.Detect == RMSDetector {
		energy := (dl*dl + dr*dr) * .5
		c.rmsSum += energy - c.rms[c.rmsIndex]
		c.rms[c.rmsIndex] = energy
		c.rmsIndex++
		if c.rmsIndex == len(c.rms) {
			c.rmsIndex = 0
		}
		level = math.Sqrt(math.Max(0, c.rmsSum/float64(len(c.rms))))
	} else {
		instant := math.Max(math.Abs(dl), math.Abs(dr))
		if instant >= c.peak || c.peakHold == 0 {
			c.peak, c.peakHold = instant, c.holdFrames
		} else {
			c.peakHold--
		}
		level = c.peak
	}
	target := c.lastTarget
	if level != c.lastLevel {
		target = c.reductionDB(level)
		c.lastLevel, c.lastTarget = level, target
	}
	coefficient := c.release
	if target > c.reduction {
		coefficient = c.attack
	}
	c.reduction += (target - c.reduction) * coefficient
	c.makeup += (c.targetMakeup - c.makeup) * c.smooth
	c.mix += (c.targetMix - c.mix) * c.smooth
	gain := fastmath.Exp2((c.makeup - c.reduction) * (math.Log2E * math.Ln10 / 20))
	outL := l*(1-c.mix) + l*gain*c.mix
	outR := r*(1-c.mix) + r*gain*c.mix
	if !finite(outL) || !finite(outR) || math.Abs(outL) > math.MaxFloat32 || math.Abs(outR) > math.MaxFloat32 {
		c.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}

func (c *Compressor) Process(left, right float32) (float32, float32) {
	return c.process(left, right, left, right, false)
}

func (c *Compressor) ProcessSidechain(left, right, sideL, sideR float32) (float32, float32) {
	return c.process(left, right, sideL, sideR, true)
}
