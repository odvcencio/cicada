package fx

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/detmath"
)

// EchoHighpass is the one-pole high-pass inside the feedback loop of the delay
// and tape echo returns, the counterpart of their damp low-pass. It subtracts
// a one-pole low-pass of the loop signal from that signal, so every pass
// through the loop removes more of the lows and the repeats thin out: the dub
// echo that fades to a whisper.
//
// An echo that never turns the filter on never runs it: Active stays false
// and the loop computes exactly what it did before the filter existed. Turning
// it on fades the subtraction in over the echo's 5 ms smoother; turning it off
// fades it out the same way, after which the filter stops and the loop is
// exact again. The cutoff glides through the same smoother. Every product is
// rounded before it is added, and the coefficient comes from detmath, so the
// filter gives the same bits on every architecture.
type EchoHighpass struct {
	coef, targetCoef     float64 // one-pole coefficient of the subtracted low-pass
	amount, targetAmount float64 // how much of that low-pass is subtracted, 0 to 1
	left, right          float64 // the low-pass state
	active               bool
}

// echoHighpassRest is the subtraction amount below which a fading-out filter
// stops: its contribution is then below -180 dB of the loop signal.
const echoHighpassRest = 1e-9

// Set selects the cutoff in Hz at sampleRate; 0 turns the filter off. A cutoff
// set while the filter is stopped takes effect at once, since there is no
// earlier cutoff to glide from, and the subtraction fades in.
func (h *EchoHighpass) Set(hz, sampleRate float64) {
	if hz <= 0 {
		h.targetAmount = 0
		return
	}
	h.targetCoef = 1 - detmath.Exp(-2*math.Pi*hz/sampleRate)
	h.targetAmount = 1
	if !h.active {
		h.coef, h.active = h.targetCoef, true
	}
}

// Reset clears the low-pass state and jumps to the targets, as the echo's own
// Reset does for its other controls.
func (h *EchoHighpass) Reset() {
	h.coef, h.amount = h.targetCoef, h.targetAmount
	h.left, h.right = 0, 0
	h.active = h.targetAmount != 0
}

// Active reports whether the filter is running.
func (h *EchoHighpass) Active() bool { return h.active }

// Advance moves the cutoff and the subtraction one frame toward their targets
// with the echo's smoothing coefficient. A filter that has faded out stops.
func (h *EchoHighpass) Advance(smooth float64) {
	if !h.active {
		return
	}
	h.coef += float64((h.targetCoef - h.coef) * smooth)
	h.amount += float64((h.targetAmount - h.amount) * smooth)
	if h.targetAmount == 0 && h.amount < echoHighpassRest {
		h.amount, h.left, h.right, h.active = 0, 0, 0, false
	}
}

// Process filters one stereo frame of the loop signal. Call it only while
// Active.
func (h *EchoHighpass) Process(left, right float64) (float64, float64) {
	h.left += float64((left - h.left) * h.coef)
	h.right += float64((right - h.right) * h.coef)
	return left - float64(h.left*h.amount), right - float64(h.right*h.amount)
}
