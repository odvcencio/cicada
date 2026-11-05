package engine

import "m31labs.dev/cicada/kernel/graph"

// graphLFOState advances only with transport playback. A seek rebases the
// absolute sample, while a stop retains the exact fractional-tick position.
type graphLFOState struct {
	clock     graph.LFOClock
	absolute  int64
	cycle     int64
	loopStart int64
}

func (e *Engine) syncGraphLFOClock() {
	s := e.graphLFO
	s.clock.Playing = e.transport.Playing()
	position := s.absolute
	if e.loopSong && e.scheduleDuration > 0 {
		cycle := e.transport.Tick() / e.scheduleDuration
		if cycle != s.cycle {
			s.cycle = cycle
			// A live tempo change can make actual playback differ from the
			// authored time map. The frame crossing the seam is loop sample 0.
			s.loopStart = s.absolute
		}
		position -= s.loopStart
	}
	s.clock.Sample = uint64(max(0, position))
}
