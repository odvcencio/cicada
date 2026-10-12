//go:build tinygo && !trackfx

package engine

// Portable default kernels refuse track-chain images. Hosts automatically load
// the separately budgeted vocal variant when an image requires this feature.
type trackEffectChain struct {
	latency int
	fault   bool
}

func newTrackEffectChain(rate int, specs []TrackEffectConfig) (*trackEffectChain, error) {
	if len(specs) > 0 {
		return nil, Error("track inserts require the vocal kernel module")
	}
	return nil, nil
}
func (c *trackEffectChain) Process(l, r float32) (float32, float32) { return l, r }
func (c *trackEffectChain) Reset()                                  {}
