package smf

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// Each region shares playback's origin. Append only onsets in this window;
// retain gates across bar windows and close them at the exclusive region end.
func appendPerformedRange(track *TrackChunk, pattern *seq.Pattern, origin, from, to, end int64, trackIndex, channel uint8) {
	p := pattern.Performance
	first := max(int64(0), (from-origin)/p.LengthTicks)
	for iteration := first; origin+iteration*p.LengthTicks < to; iteration++ {
		base := origin + iteration*p.LengthTicks
		for _, note := range p.Notes {
			at, off := base+note.Tick, min(base+note.EndTick, end)
			if at < from || at >= to || off <= at {
				continue
			}
			track.Notes = append(track.Notes, Note{Track: trackIndex + 1, Tick: at, Dur: off - at, Note: uint8(int(note.Note) + int(pattern.Transpose)), Vel: note.Velocity, Chan: channel})
		}
	}
}

func closePerformedAt(track *TrackChunk, tick int64) {
	for i := range track.Notes {
		note := &track.Notes[i]
		if note.Tick < tick && note.Tick+note.Dur > tick {
			note.Dur = tick - note.Tick
		}
	}
}

// A scene selection uses the engine's global phase. Re-entering within a
// held gate retriggers at the boundary, preserving its remaining duration.
func appendPerformedChase(track *TrackChunk, pattern *seq.Pattern, origin, tick, end int64, trackIndex, channel uint8) {
	p := pattern.Performance
	base := origin + (tick-origin)/p.LengthTicks*p.LengthTicks
	for _, note := range p.Notes {
		if base+note.Tick < tick && base+note.EndTick > tick {
			track.Notes = append(track.Notes, Note{Track: trackIndex + 1, Tick: tick, Dur: min(base+note.EndTick, end) - tick, Note: uint8(int(note.Note) + int(pattern.Transpose)), Vel: note.Velocity, Chan: channel})
		}
	}
}

// Scene transitions precede chain advances at a shared boundary, so callers
// resolve the outgoing chain at the preceding tick.
func chainPosition(bank *engine.PatternBank, tick int64) (int, int64) {
	var period int64
	for _, slot := range bank.Chain {
		period += bank.Slots[slot].DurationTicks()
	}
	if period == 0 {
		return -1, 0
	}
	base := tick / period * period
	for _, slot := range bank.Chain {
		length := bank.Slots[slot].DurationTicks()
		if tick < base+length {
			return int(slot), base
		}
		base += length
	}
	return -1, 0
}
