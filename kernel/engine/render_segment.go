package engine

import "m31labs.dev/cicada/kernel/seq"

// parallelSegmentFrames ends before any source or insert control can change.
// Mixer smoothing, returns, transport and ordered reduction stay per-sample.
func (e *Engine) parallelSegmentFrames(remaining int) int {
	count := remaining
	sample := e.transport.Sample()
	clock := e.transport.Clock()
	limitSample := func(boundary int64) {
		if boundary > sample && boundary-sample < int64(count) {
			count = int(boundary - sample)
		}
	}
	limitTick := func(tick int64) { limitSample(clock.SampleAtTick(tick)) }
	for track := 0; track < e.tracks; track++ {
		p := &e.patterns[track]
		if p.eventIndex < p.eventCount {
			limitSample(p.eventStorage()[p.eventIndex].event.Sample)
		}
		d := &e.director[track]
		if d.stop && d.frames != 0 {
			// advanceDirector stops/reset voices before rendering the frame
			// after its final gain step, even with a stopped transport.
			count = min(count, int(d.frames-d.elapsed)+1)
		}
		if e.transport.Playing() {
			if p.chainArmed {
				limitTick(p.chainDue)
			}
			if d.stinger {
				limitTick(d.endTick)
			}
		}
	}
	if !e.transport.Playing() {
		return max(1, count)
	}
	for i := 0; i < e.pendingLen; i++ {
		limitTick(e.pending[i].Tick)
	}
	if e.songMode {
		limitTick(e.songEndTick)
	}
	limitTick((e.transport.Tick()/seq.TicksPerBar + 1) * seq.TicksPerBar)
	if tempo, tick := e.transport.PendingTempo(); tempo != 0 {
		limitTick(tick)
	}
	if e.automationIndex < len(e.automation) {
		tick := e.automation[e.automationIndex].Tick
		if e.loopSong && e.automationCycle > 0 {
			tick += e.transport.Tick() / e.automationCycle * e.automationCycle
		}
		limitTick(tick)
	}
	return max(1, count)
}
