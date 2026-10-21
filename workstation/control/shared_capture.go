package control

import "m31labs.dev/cicada/host/midi"

// BindShared keeps the controller's initial state at the first captured note.
// Input identity remains private; committing gives the lane a musical name.
func (r *Recording) BindShared(note int, value midi.SharedControls, lead ...NoteExpression) int {
	for index, group := range r.Groups {
		if group.ID == value.ID {
			r.Notes[note].Group = uint16(index + 1)
			return 0
		}
	}
	points, added := append([]NoteExpression(nil), lead...), 0
	if len(points) == 0 {
		points = []NoteExpression{{Tick: r.Notes[note].Tick, PitchCents: value.PitchCents, Pressure: value.Pressure, Timbre: value.Timbre}}
		added = 1
	}
	r.Groups = append(r.Groups, RecordedGroup{ID: value.ID, Device: value.Device, Channel: value.Channel, Points: points})
	r.Notes[note].Group = uint16(len(r.Groups))
	return added
}

// AddShared records movement even after the last note-off. It never stretches
// a gate or rewrites a member's local expression to bake in master controls.
func (r *Recording) AddShared(value midi.SharedControls, tick int64) bool {
	for index := range r.Groups {
		group := &r.Groups[index]
		if group.ID != value.ID {
			continue
		}
		tick = max(tick, group.Points[len(group.Points)-1].Tick)
		group.Points = append(group.Points, NoteExpression{Tick: tick, PitchCents: value.PitchCents, Pressure: value.Pressure, Timbre: value.Timbre})
		return true
	}
	return false
}

func (r Recording) ExpressionPointCount() int {
	count := 0
	for _, note := range r.Notes {
		count += len(note.Expressions)
	}
	for _, group := range r.Groups {
		count += len(group.Points)
	}
	return count
}
