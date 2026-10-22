package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/seq"
)

// A song's tempo changes only at scene boundaries, so its sample positions are
// known before playback. Clip and prepared-voice seeks measure the time between
// two ticks with this chain instead of one fixed tempo. Each clock re-anchors
// at its tick the way the transport does, so a seek and playing through agree
// to the sample. Without a scene tempo the chain is empty and the current
// transport tempo applies.
// sceneTempoMilli converts a scene tempo setting to thousandths of a BPM.
func sceneTempoMilli(setting SceneSetting) int64 {
	return int64(setting.Value*1000 + .5) // tempos are positive
}

// stepClock moves a clock to a new tempo at a tick, as the transport does.
func stepClock(c seq.Clock, tick, bpmMilli int64) seq.Clock {
	c.AnchorSample, c.AnchorTick, c.BPMMilli = c.SampleAtTick(tick), tick, bpmMilli
	return c
}

func (e *Engine) buildTempoChain() error {
	if len(e.schedule) == 0 {
		return nil
	}
	clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.bpmMilli}
	chain := []seq.Clock{clock}
	for _, entry := range e.schedule {
		var milli int64
		if e.placementSchedule {
			if entry.Kind == ScheduleTempo {
				milli = int64(entry.ID)
			}
		} else if settings := e.scenes[entry.Scene].Settings; len(settings) > 0 && settings[0].ID == kernel.ParamGlobalTempo {
			// Validation requires tempo first, so other settings need no scan.
			milli = sceneTempoMilli(settings[0])
		}
		if milli == 0 {
			continue
		}
		e.songTempo = true
		if milli == clock.BPMMilli {
			continue
		}
		next := stepClock(clock, entry.Tick, milli)
		if entry.Tick == clock.AnchorTick {
			chain[len(chain)-1] = next
		} else {
			chain = append(chain, next)
		}
		clock = next
	}

	e.tempoCycleSamples = clock.SampleAtTick(e.scheduleDuration)
	if len(chain) > 1 || chain[0].BPMMilli != e.bpmMilli {
		e.tempoChain = chain
	}
	return nil
}

// chainStep finds the clock that governs an absolute song tick: its loop cycle,
// its index in the chain, and the tick within the cycle. A looping song restarts
// the chain each cycle, and chain[0] holds the tempo at tick 0.
func (e *Engine) chainStep(tick int64) (cycle int64, index int, local int64) {
	if e.loopSong {
		cycle = tick / e.scheduleDuration
	}
	local = tick - cycle*e.scheduleDuration
	for i, clock := range e.tempoChain {
		if clock.AnchorTick <= local {
			index = i
		}
	}
	return
}

// songSampleAtTick is the sample at which an absolute tick plays, counted from
// the start of the song.
func (e *Engine) songSampleAtTick(tick int64) int64 {
	if len(e.tempoChain) == 0 {
		clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.transport.BPMMilli()}
		return clock.SampleAtTick(tick)
	}
	// Play through from the start with the transport's own rules, including the
	// loop seams that reanchor nothing, so a seek agrees with playing through.
	return e.authoredFrames(0, tick)
}

// TempoAtTick is the tempo, in thousandths of a BPM, that plays at a song tick
// after a seek to it: the score tempo, changed by every scene tempo at or before
// the tick. A looping song starts each pass at the score tempo.
func (e *Engine) TempoAtTick(tick int64) int64 {
	if len(e.tempoChain) == 0 {
		return e.bpmMilli
	}
	_, index, _ := e.chainStep(tick)
	return e.tempoChain[index].BPMMilli
}

// FramesBetweenTicks counts the frames from one song tick to a later one when
// playback starts at the first, so hosts place bar boundaries with the kernel's
// own clock and never keep a copy of the tempo map. It follows the transport:
// each tempo step re-anchors at the sample where its tick first plays.
//
// After a manual launch or a tempo command moved the tempo off the authored
// chain, the answer follows the live transport instead: the clock the engine
// is actually playing, counted from the sample of the last seek.
func (e *Engine) FramesBetweenTicks(from, to int64) int64 {
	if e.liveTempo && from == e.seekTick && to > from {
		return e.transport.Clock().SampleAtTick(to) - e.seekSample
	}
	return e.authoredFrames(from, to)
}

// authoredFrames follows the authored chain only, whatever the live transport did.
func (e *Engine) authoredFrames(from, to int64) int64 {
	clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.TempoAtTick(from), AnchorTick: from}
	if len(e.tempoChain) != 0 {
		cycle, index, _ := e.chainStep(from)
		n := len(e.tempoChain)
		// Walk the steps after `from`, wrapping into the next cycle when looping.
		for step := cycle*int64(n) + int64(index) + 1; ; step++ {
			if !e.loopSong && step >= int64(n) {
				break
			}
			at := step/int64(n)*e.scheduleDuration + e.tempoChain[step%int64(n)].AnchorTick
			if at > to {
				break
			}
			if bpm := e.tempoChain[step%int64(n)].BPMMilli; bpm != clock.BPMMilli {
				clock = stepClock(clock, at, bpm)
			}
		}
	}
	if to <= from {
		return 0
	}
	return clock.SampleAtTick(to)
}

// LiveClock is the clock the transport is playing now, with the sample it is
// anchored at counted in the engine's own frames. Hosts that place bar
// boundaries follow it after a manual launch changes the tempo.
func (e *Engine) LiveClock() seq.Clock { return e.transport.Clock() }

// TransportSample counts the frames the engine has rendered.
func (e *Engine) TransportSample() int64 { return e.transport.Sample() }
