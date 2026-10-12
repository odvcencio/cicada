package engine

import (
	"math"

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
	return int64(math.Round(float64(setting.Value) * 1000))
}

func (e *Engine) buildTempoChain() error {
	if len(e.schedule) == 0 || e.placementSchedule {
		return nil
	}
	clock, err := seq.NewClock(e.sampleRate, e.bpmMilli)
	if err != nil {
		return err
	}
	chain := []seq.Clock{clock}
	for _, entry := range e.schedule {
		for _, setting := range e.scenes[entry.Scene].Settings {
			if setting.ID != kernel.ParamGlobalTempo {
				continue
			}
			milli := sceneTempoMilli(setting)
			if milli == clock.BPMMilli {
				continue
			}
			if clock, err = clock.Reanchor(entry.Tick, milli); err != nil {
				return err
			}
			chain = append(chain, clock)
		}
	}
	e.tempoCycleSamples = clock.SampleAtTick(e.scheduleDuration)
	if len(chain) > 1 {
		e.tempoChain = chain
	}
	return nil
}

// chainClock splits an absolute song tick into its loop cycle and the clock
// that governs it. A looping song restarts at the score tempo each cycle. The
// chain must not be empty.
func (e *Engine) chainClock(tick int64) (cycle int64, clock seq.Clock, local int64) {
	if e.loopSong {
		cycle = tick / e.scheduleDuration
	}
	local = tick - cycle*e.scheduleDuration
	clock = e.tempoChain[0]
	for _, candidate := range e.tempoChain {
		if candidate.AnchorTick <= local {
			clock = candidate
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
	cycle, clock, local := e.chainClock(tick)
	return cycle*e.tempoCycleSamples + clock.SampleAtTick(local)
}

// TempoAtTick is the tempo, in thousandths of a BPM, that plays at a song tick
// after a seek to it: the score tempo, changed by every scene tempo at or before
// the tick. A looping song starts each pass at the score tempo.
func (e *Engine) TempoAtTick(tick int64) int64 {
	if len(e.tempoChain) == 0 {
		return e.bpmMilli
	}
	_, clock, _ := e.chainClock(tick)
	return clock.BPMMilli
}

// nextTempoStep finds the first tempo change after a tick, including the
// return to the score tempo when a looping song wraps.
func (e *Engine) nextTempoStep(after int64) (tick, bpmMilli int64, ok bool) {
	var base int64
	if e.loopSong {
		base = after / e.scheduleDuration * e.scheduleDuration
	}
	local := after - base
	for _, step := range e.tempoChain[1:] {
		if step.AnchorTick > local {
			return base + step.AnchorTick, step.BPMMilli, true
		}
	}
	if !e.loopSong {
		return 0, 0, false
	}
	bpmMilli = e.bpmMilli
	if first := e.tempoChain[1]; first.AnchorTick == 0 {
		bpmMilli = first.BPMMilli
	}
	return base + e.scheduleDuration, bpmMilli, true
}

// FramesBetweenTicks counts the frames from one song tick to a later one when
// playback starts at the first, so hosts place bar boundaries with the kernel's
// own clock and never keep a copy of the tempo map. It follows the transport:
// each tempo step re-anchors at the sample where its tick first plays.
func (e *Engine) FramesBetweenTicks(from, to int64) int64 {
	if to <= from {
		return 0
	}
	clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.TempoAtTick(from), AnchorTick: from}
	for after := from; len(e.tempoChain) != 0; {
		at, bpmMilli, ok := e.nextTempoStep(after)
		if !ok || at > to {
			break
		}
		after = at
		if bpmMilli != clock.BPMMilli {
			clock, _ = clock.Reanchor(at, bpmMilli)
		}
	}
	return clock.SampleAtTick(to)
}
