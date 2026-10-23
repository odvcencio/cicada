package smf

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// Each region shares playback's origin. Append only onsets in this window;
// retain gates across bar windows and close them at the exclusive region end.
func appendPerformedRange(track *TrackChunk, pattern *seq.Pattern, origin, from, to, end int64, trackIndex, channel uint8) {
	p := pattern.Performance
	groupBase := appendSharedRange(track, p, origin, from, min(to, end))
	first := max(int64(0), (from-origin)/p.LengthTicks)
	for iteration := first; origin+iteration*p.LengthTicks < to; iteration++ {
		base := origin + iteration*p.LengthTicks
		for _, note := range p.Notes {
			at, off := base+note.Tick, min(base+note.EndTick, end)
			if at < from || at >= to || off <= at {
				continue
			}
			gesture := performedMIDIGesture(note, 0, off-at)
			if note.Group != 0 {
				gesture.Group = groupBase + int(note.Group)
			}
			track.Notes = append(track.Notes, Note{Track: trackIndex + 1, Tick: at, Dur: off - at, Note: uint8(int(note.Note) + int(pattern.Transpose)), Vel: note.Velocity, Chan: channel, Gesture: gesture})
		}
	}
}

func closePerformedAt(track *TrackChunk, tick int64) {
	track.performedGroups = nil
	for i := range track.Notes {
		note := &track.Notes[i]
		if note.Tick < tick && note.Tick+note.Dur > tick {
			note.Dur = tick - note.Tick
			if note.Gesture != nil {
				end := len(note.Gesture.Points)
				for end > 0 && note.Gesture.Points[end-1].Tick > note.Dur {
					end--
				}
				note.Gesture.Points = note.Gesture.Points[:end]
			}
		}
	}
}

// A scene selection uses the engine's global phase. Re-entering within a
// held gate retriggers at the boundary, preserving its remaining duration.
func appendPerformedChase(track *TrackChunk, pattern *seq.Pattern, origin, tick, end int64, trackIndex, channel uint8) {
	p := pattern.Performance
	groupBase := appendSharedRange(track, p, origin, tick, tick)
	base := origin + (tick-origin)/p.LengthTicks*p.LengthTicks
	for _, note := range p.Notes {
		if base+note.Tick < tick && base+note.EndTick > tick {
			duration := min(base+note.EndTick, end) - tick
			gesture := performedMIDIGesture(note, tick-base-note.Tick, duration)
			if note.Group != 0 {
				gesture.Group = groupBase + int(note.Group)
			}
			track.Notes = append(track.Notes, Note{Track: trackIndex + 1, Tick: tick, Dur: duration, Note: uint8(int(note.Note) + int(pattern.Transpose)), Vel: note.Velocity, Chan: channel, Gesture: gesture})
		}
	}
}

func performedMIDIGesture(note seq.PerformedNote, skip, duration int64) *NoteGesture {
	if len(note.Expressions) == 0 && note.Group == 0 {
		return nil
	}
	var points []seq.PerformedExpression
	for _, point := range note.Expressions {
		if point.Tick <= skip {
			point.Tick = 0
			if len(points) == 0 {
				points = append(points, point)
			} else {
				points[0] = point
			}
		} else if point.Tick < skip+duration || note.Group != 0 && point.Tick == skip+duration {
			point.Tick -= skip
			points = append(points, point)
		}
	}
	if len(points) == 0 && note.Group == 0 {
		return nil
	}
	return &NoteGesture{Points: points}
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
