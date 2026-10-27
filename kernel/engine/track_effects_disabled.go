//go:build tinygo && !trackfx

package engine

import "m31labs.dev/cicada/kernel"

// Portable default kernels refuse track-chain images. Hosts automatically load
// the separately budgeted vocal variant when an image requires this feature.
const ampControlsEnabled = false

type trackEffectChain struct {
	latency int
	fault   bool
}

func newTrackEffectChain(rate int, specs []TrackEffectConfig) (*trackEffectChain, error) {
	if len(specs) > 0 {
		return nil, engineError("track inserts require the vocal kernel module")
	}
	return nil, nil
}
func (c *trackEffectChain) Process(l, r float32) (float32, float32) { return l, r }
func (c *trackEffectChain) Reset()                                  {}

func (c *trackEffectChain) hasAmp() bool                    { return false }
func (c *trackEffectChain) ampControls() ([3]float64, bool) { return [3]float64{}, false }
func (c *trackEffectChain) setAmpControls([3]float64, bool) error {
	return engineError("amp controls require the track FX kernel")
}
func (c *trackEffectChain) setAmpParam(kernel.ParamID, float32, bool) error {
	return engineError("amp controls require the track FX kernel")
}
