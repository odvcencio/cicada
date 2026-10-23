package liveplay

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/musictime"
)

// nextTempoStep runs on the render thread. Tempo steps are bar-aligned, and
// even the fastest bar is longer than the 350 ms observation horizon. Cache
// the authored lookup once per engine/bar rather than scan it every block.
func (p *Player) nextTempoStep() musictime.ObservedTempoStep {
	if !p.current.Engine.Playing() || p.launches.Load() != nil || len(p.offers) != 0 {
		return musictime.ObservedTempoStep{}
	}
	boundary := (p.bar + 1) * seq.TicksPerBar
	if r := p.playbackRange; r != nil && boundary >= r.EndTick {
		return musictime.ObservedTempoStep{}
	}
	remaining := p.clock.SampleAtTick(boundary) - p.sample
	if remaining < 0 || remaining*1000 > int64(p.rate)*350 {
		return musictime.ObservedTempoStep{}
	}
	if p.nextClockEngine != p.current.Engine || p.nextClockBar != p.bar {
		p.nextClockEngine, p.nextClockBar = p.current.Engine, p.bar
		p.nextClockTempo = p.current.Engine.TempoAtTick(boundary)
	}
	if p.nextClockTempo == p.clock.BPMMilli {
		return musictime.ObservedTempoStep{}
	}
	return musictime.ObservedTempoStep{AfterNanos: remaining * 1_000_000_000 / int64(p.rate), Tick: boundary, BPMMilli: p.nextClockTempo}
}
