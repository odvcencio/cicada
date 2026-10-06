// Package pro provides an opt-in stereo mix chain. All render-loop storage is
// allocated by constructors; controls are immutable until a new chain is built.
package pro

import (
	"math"

	"m31labs.dev/cicada/kernel/fx"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrSampleRate Error = "mix chain supports 44100, 48000, and 96000 Hz"
	ErrParams     Error = "mix chain parameter is invalid or out of range"
	ErrInput      Error = "mix chain input contains a non-finite sample"
)

func validRate(rate int) bool { return rate == 44100 || rate == 48000 || rate == 96000 }
func finite(x float64) bool   { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func clean(x float64) float64 {
	if math.Abs(x) < 1e-30 {
		return 0
	}
	return x
}
func dbGain(db float64) float64 { return math.Pow(10, db/20) }

// Params describes EQ -> compression -> transients -> width -> room -> limiter.
// Zero Params is an exact zero-latency bypass. Width.Enabled distinguishes
// width zero (mono) from the zero-value bypass. No limiter is enabled implicitly.
type Params struct {
	EQ               [4]Band
	EnableCompressor bool
	Compressor       fx.CompParams
	Transient        TransientParams
	Width            WidthParams
	Reverb           ReverbParams
	EnableLimiter    bool
	Limiter          LimiterParams
}

// DefaultParams provides sensible controls with all processors bypassed.
func DefaultParams() Params {
	return Params{
		Compressor: fx.DefaultCompParams(),
		Transient:  DefaultTransientParams(),
		Width:      WidthParams{Amount: 1},
		Reverb:     DefaultReverbParams(),
		Limiter:    DefaultLimiterParams(),
	}
}

// Chain is single-owner audio state. Process and Reset allocate no memory.
type Chain struct {
	eq         *EQ
	compressor *fx.Compressor
	transient  *Transient
	width      *Width
	reverb     *Reverb
	limiter    *Limiter
	fault      bool
}

func New(sampleRate int, params Params) (*Chain, error) {
	if !validRate(sampleRate) {
		return nil, ErrSampleRate
	}
	c := &Chain{}
	var err error
	c.eq, err = NewEQ(sampleRate, params.EQ)
	if err != nil {
		return nil, err
	}
	if params.EnableCompressor {
		c.compressor, err = fx.NewCompressor(sampleRate)
		if err != nil {
			return nil, err
		}
		if err = c.compressor.SetParams(params.Compressor); err != nil {
			return nil, err
		}
		c.compressor.Reset()
	}
	if params.Transient.Enabled {
		c.transient, err = NewTransient(sampleRate, params.Transient)
		if err != nil {
			return nil, err
		}
	}
	if params.Width.Enabled {
		c.width, err = NewWidth(sampleRate, params.Width)
		if err != nil {
			return nil, err
		}
	}
	if params.Reverb.Enabled {
		c.reverb, err = NewReverb(sampleRate, params.Reverb)
		if err != nil {
			return nil, err
		}
	}
	if params.EnableLimiter {
		c.limiter, err = NewLimiter(sampleRate, params.Limiter)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Chain) Process(left, right float32) (float32, float32) {
	if c.fault || !finite(float64(left)) || !finite(float64(right)) {
		c.fault = true
		return 0, 0
	}
	l, r := c.eq.Process(left, right)
	if c.compressor != nil {
		l, r = c.compressor.Process(l, r)
		c.fault = c.compressor.Fault()
	}
	if c.transient != nil {
		l, r = c.transient.Process(l, r)
	}
	if c.width != nil {
		l, r = c.width.Process(l, r)
	}
	if c.reverb != nil {
		l, r = c.reverb.Process(l, r)
		c.fault = c.fault || c.reverb.Fault()
	}
	if c.limiter != nil {
		l, r = c.limiter.Process(l, r)
		c.fault = c.fault || c.limiter.Fault()
	}
	if !finite(float64(l)) || !finite(float64(r)) {
		c.fault = true
	}
	if c.fault {
		return 0, 0
	}
	return l, r
}

func (c *Chain) Reset() {
	c.eq.Reset()
	if c.compressor != nil {
		c.compressor.Reset()
	}
	if c.transient != nil {
		c.transient.Reset()
	}
	if c.width != nil {
		c.width.Reset()
	}
	if c.reverb != nil {
		c.reverb.Reset()
	}
	if c.limiter != nil {
		c.limiter.Reset()
	}
	c.fault = false
}

func (c *Chain) Fault() bool { return c.fault }

func (c *Chain) LatencyFrames() int {
	frames := 0
	if c.transient != nil {
		frames += c.transient.LatencyFrames()
	}
	if c.limiter != nil {
		frames += c.limiter.LatencyFrames()
	}
	return frames
}

// Latency is an alias useful to hosts which count delay in sample frames.
func (c *Chain) Latency() int { return c.LatencyFrames() }
