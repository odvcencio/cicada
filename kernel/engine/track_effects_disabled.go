//go:build tinygo && !trackfx

package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/fx/chorus"
)

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

func (c *trackEffectChain) monitorAmpTargets() (AmpTargets, bool) { return AmpTargets{}, false }
func (c *trackEffectChain) hasAmp() bool                          { return false }
func (c *trackEffectChain) ampControls() ([3]float64, bool)       { return [3]float64{}, false }
func (c *trackEffectChain) setAmpControls([3]float64, bool) error {
	return engineError("amp controls require the track FX kernel")
}
func (c *trackEffectChain) setAmpParam(kernel.ParamID, float32, bool) error {
	return engineError("amp controls require the track FX kernel")
}
func (c *trackEffectChain) chorusParams() (chorus.Params, bool) { return chorus.Params{}, false }
func (c *trackEffectChain) setChorusParam(kernel.ParamID, float32, bool) error {
	return engineError("chorus requires the modular kernel")
}
func (c *trackEffectChain) setChorusControls(chorus.Params, bool) error {
	return engineError("chorus requires the modular kernel")
}
