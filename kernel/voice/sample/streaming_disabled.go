//go:build tinygo

package sample

// Browser PCM remains resident until worker-to-worklet page transfer exists.
// Keeping these no-ops separate removes native cache/SRC branches from core.
type streamSource struct{}

func (v *Voice) hasStream() bool                                                     { return false }
func (*streamSource) SeekFrame(int64, int) error                                     { return nil }
func (*streamSource) AdvanceFrame(int64)                                             {}
func (*streamSource) FilterOutput(l, r float32, ok, inRange bool) (float32, float32) { return l, r }
func (*streamSource) ReadFrame(int64) (float32, float32, bool)                       { return 0, 0, true }
func (*streamSource) Stop()                                                          {}
