package analysis

import (
	"fmt"
	"math"
)

const (
	correlationTau    = 0.3   // seconds
	correlationMinMSQ = 1e-9  // -90 dBFS mean square
	correlationDenorm = 1e-30 // flush level so decayed sums never go denormal
)

// Correlation tracks the stereo correlation coefficient over an exponential
// window with a 300 ms time constant. It is not safe for concurrent use.
type Correlation struct {
	decay         float64
	norm          float64 // 1 - decay: turns a decayed sum into a mean
	sll, srr, slr float64
}

// NewCorrelation builds a tracker for the given sample rate.
func NewCorrelation(sampleRate int) (*Correlation, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("analysis: sample rate %d Hz is not positive", sampleRate)
	}
	decay := math.Exp(-1 / (correlationTau * float64(sampleRate)))
	return &Correlation{decay: decay, norm: 1 - decay}, nil
}

// Reset clears the window.
func (c *Correlation) Reset() { c.sll, c.srr, c.slr = 0, 0, 0 }

// Process adds frames. If the slices differ in length, the shorter one sets
// the frame count. Non-finite samples count as 0.
func (c *Correlation) Process(left, right []float32) {
	n := min(len(left), len(right))
	sll, srr, slr, d := c.sll, c.srr, c.slr, c.decay
	for i := 0; i < n; i++ {
		l, r := float64(left[i]), float64(right[i])
		if l != l || r != r || l-l != 0 || r-r != 0 {
			l, r = 0, 0
		}
		sll = d*sll + l*l
		srr = d*srr + r*r
		slr = d*slr + l*r
	}
	if sll < correlationDenorm {
		sll = 0
	}
	if srr < correlationDenorm {
		srr = 0
	}
	if math.Abs(slr) < correlationDenorm {
		slr = 0
	}
	c.sll, c.srr, c.slr = sll, srr, slr
}

// Value returns r in [-1, 1]. ok is false when either channel's windowed mean
// square is below -90 dBFS, where the ratio is not meaningful.
func (c *Correlation) Value() (r float64, ok bool) {
	if c.sll*c.norm < correlationMinMSQ || c.srr*c.norm < correlationMinMSQ {
		return 0, false
	}
	r = c.slr / math.Sqrt(c.sll*c.srr)
	return math.Max(-1, math.Min(1, r)), true
}
