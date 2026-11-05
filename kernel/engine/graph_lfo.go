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
			s.loopStart = e.songSampleAtTick(cycle * e.scheduleDuration)
		}
		position -= s.loopStart
	}
	s.clock.Sample = uint64(max(0, position))
}
