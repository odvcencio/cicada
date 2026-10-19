package seq

// Performance stores independent gates in musical time. It is prepared and
// validated before playback, then remains immutable in the audio callback.
type Performance struct {
	LengthTicks int64
	Notes       []PerformedNote
}

const MaxPerformanceNotes = 512

type PerformedNote struct {
	Tick, EndTick  int64
	Note, Velocity uint8
}

func (p *Performance) Validate() error {
	if p == nil || p.LengthTicks < 30 || p.LengthTicks > 1<<40 || len(p.Notes) == 0 || len(p.Notes) > MaxPerformanceNotes {
		return Error("a performance needs 1 to 512 notes and a valid loop length")
	}
	for i, note := range p.Notes {
		if note.Tick < 0 || note.EndTick <= note.Tick || note.EndTick > p.LengthTicks || note.Note > 127 || note.Velocity < 1 || note.Velocity > 127 {
			return Error("performed note pitch, velocity or gate is invalid")
		}
		for _, other := range p.Notes[:i] {
			if other.Note == note.Note && other.Tick < note.EndTick && note.Tick < other.EndTick {
				return Error("overlapping gates for the same performed pitch need separate tracks")
			}
		}
	}
	return nil
}

// PerformanceEventsInBlock preserves every onset, release and velocity. Gates
// ending at the loop boundary belong to the preceding iteration. Offs precede
// ons at the same sample, including a repeated pitch across the loop boundary.
// Like the step scheduler, this function never allocates and reports overflow.
func PerformanceEventsInBlock(p *Performance, clock Clock, track, slot uint8, startTick, startSample int64, frames int, transpose int8, dst []Event, gates bool) (written int, overflow bool) {
	if p == nil || p.LengthTicks < 30 || frames < 1 {
		return 0, false
	}
	first := max(int64(0), (clock.TickAtSample(startSample)-startTick)/p.LengthTicks-1)
	last := (clock.TickAtSample(startSample+int64(frames))-startTick)/p.LengthTicks + 1
	for iteration := first; iteration <= last; iteration++ {
		base := startTick + iteration*p.LengthTicks
		for i, note := range p.Notes {
			for kind := NoteOn; kind <= NoteOff; kind++ {
				if kind == NoteOff && !gates {
					continue
				}
				tick := base + note.Tick
				if kind == NoteOff {
					tick = base + note.EndTick
				}
				if tick < clock.AnchorTick {
					continue
				}
				sample := clock.SampleAtTick(tick)
				if sample < startSample || sample >= startSample+int64(frames) {
					continue
				}
				if written == len(dst) {
					overflow = true
					continue
				}
				dst[written] = Event{Kind: kind, NoteID: iteration*MaxPerformanceNotes + int64(i) + 1,
					Tick: tick, Sample: sample, Offset: int32(sample - startSample), Track: track, Slot: slot,
					Note: uint8(int(note.Note) + int(transpose)), Velocity: note.Velocity}
				written++
			}
		}
	}
	for i := 1; i < written; i++ {
		value, j := dst[i], i
		for j > 0 && eventBefore(value, dst[j-1]) {
			dst[j] = dst[j-1]
			j--
		}
		dst[j] = value
	}
	return written, overflow
}
