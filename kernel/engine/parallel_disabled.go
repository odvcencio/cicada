//go:build tinygo || js

package engine

// Browser kernels retain the serial render path and never create workers or
// per-track block buffers. This preserves the portable core's resource budget.
type parallelState struct{}

// Browser kernels never create workers, so the wait floor is ignored.
type parallelConfig struct{}

func (e *Engine) configureParallel(*Config) error { return nil }

func (e *Engine) EnableParallel(workers int) error {
	if workers < 0 || workers > 4 {
		return engineError("parallel DSP requires zero to four render lanes")
	}
	return nil
}
func (e *Engine) parallelAvailable() bool                               { return false }
func (e *Engine) ParallelWorkers() int                                  { return 1 }
func (e *Engine) ParallelPriority() string                              { return "serial" }
func (e *Engine) parallelRetired() bool                                 { return false }
func (e *Engine) parallelQuiescent() bool                               { return true }
func (e *Engine) parallelPrepareSegment(start, count int) (int, uint16) { return -1, 0 }
func (e *Engine) renderedTrack(track, frame int) (float32, float32, uint16) {
	return e.nextRawTrack(track)
}
func (e *Engine) parallelEndSegment() {}
func (e *Engine) retireParallel()     {}
func (e *Engine) closeParallel()      {}
