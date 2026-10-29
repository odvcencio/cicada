//go:build js && wasm

package main

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/workstation/control"
)

func (u *liveUI) legacyHeld(h liveHeld) int {
	n := 0
	for id, held := range u.held {
		if held == h && u.wireIDs[id] == 0 {
			n++
		}
	}
	return n
}

func (u *liveUI) midiNote(event midi.NoteMessage, at *control.NoteTime) {
	id := fmt.Sprintf("midi:%s:%d", event.Device, event.ID)
	switch event.Type {
	case "note-on":
		track := u.selected("[data-live-acid]")
		if event.Channel == 9 && !event.MPE {
			track = u.selected("[data-live-drums]")
			if !control.GMDrum(int(event.Note)) {
				return
			}
		}
		u.noteInputOnAt(id, track, int(event.Note), int(event.Velocity), event.ID, event.Channel, at)
		if encoded, ok := u.active[id]; ok && event.Shared != nil && u.gesturePoints < control.MaxExpressionPoints {
			i, n := encoded/1024, encoded%1024
			u.capturedPoints(u.takes[i].BindShared(n, *event.Shared, u.pendingShared[event.Shared.ID]...))
			delete(u.pendingShared, event.Shared.ID)
		}
	case "note-off":
		u.noteOffAt(id, at)
		return
	}
	h, ok := u.held[id]
	if !ok {
		return
	}
	local := midi.NoteControls{PitchCents: event.PitchCents, Pressure: event.Pressure, Timbre: event.Timbre}
	if event.Local != nil {
		local = *event.Local
	}
	if event.Type == "note-on" && event.Shared != nil && u.props.ExpressionTracks[h.Track] {
		if u.monitorGroups == nil {
			u.monitorGroups = map[uint64]liveMIDIGroup{}
		}
		group, exists := u.monitorGroups[event.Shared.ID]
		if !exists && len(u.monitorGroups) >= 512 {
			u.status.Set("Shared input limit reached. Disconnect MIDI before connecting more inputs.")
			return
		}
		if !exists {
			group = liveMIDIGroup{Device: event.Device, Tracks: map[string]bool{}}
			u.monitorGroups[event.Shared.ID] = group
		}
		group.Tracks[h.Track] = true
		u.sharedLease = true // Shared addresses outlive gates and device disconnects.
		u.queue("live", map[string]string{"type": "note-group", "track": h.Track, "noteId": strconv.Itoa(int(event.ID)), "groupId": strconv.FormatUint(event.Shared.ID, 10)})
		u.monitorShared(*event.Shared)
	}
	if shared := event.Shared; shared != nil && !u.props.ExpressionTracks[h.Track] && (shared.PitchCents != 0 || shared.Pressure != 0 || shared.Timbre != .5) {
		u.status.Set("This sound plays notes without gesture monitoring. Recorded gestures remain in the retained take; choose an authored synth to apply them, or acid for bend and vibrato.")
	}
	if !event.SharedOnly && (event.Type != "note-on" || local.PitchCents != 0 || local.Pressure != 0 || local.Timbre != .5) {
		if u.props.ExpressionTracks[h.Track] {
			u.queue("live", map[string]string{"type": "note-expression", "track": h.Track, "noteId": strconv.Itoa(int(event.ID)), "pitchCents": strconv.FormatFloat(local.PitchCents, 'f', -1, 64), "pressure": strconv.FormatFloat(local.Pressure, 'f', -1, 64), "timbre": strconv.FormatFloat(local.Timbre, 'f', -1, 64)})
		} else {
			u.status.Set("This sound plays notes without gesture monitoring. Recorded gestures remain in the retained take; choose an authored synth to apply them, or acid for bend and vibrato.")
		}
	}
	if event.SharedOnly || event.Type == "note-on" && local.PitchCents == 0 && local.Pressure == 0 && local.Timbre == .5 {
		return
	}
	if encoded, ok := u.active[id]; ok && u.gesturePoints < control.MaxExpressionPoints {
		i, n := encoded/1024, encoded%1024
		tick := u.tick()
		if at != nil {
			tick = at.Tick
		}
		u.takes[i].Notes[n].AddExpression(control.NoteExpression{Tick: tick, PitchCents: local.PitchCents, Pressure: local.Pressure, Timbre: local.Timbre})
		u.capturedPoints(1)
	}
}

func (u *liveUI) capturedPoints(count int) {
	u.gesturePoints += count
	u.takeDirty = true
	select {
	case u.checkpointWake <- struct{}{}:
	default:
	}
	if u.gesturePoints >= control.MaxExpressionPoints {
		u.recording = false
		u.status.Set("Gesture input limit reached. Finish this take before recording more.")
		u.syncControls()
	}
}

func (u *liveUI) midiShared(value midi.SharedControls, at *control.NoteTime) {
	u.monitorShared(value)
	if !u.recording {
		return
	}
	tick := u.tick()
	if at != nil {
		tick = at.Tick
	}
	matched := false
	for index := range u.takes {
		if u.gesturePoints >= control.MaxExpressionPoints {
			return
		}
		if u.takes[index].AddShared(value, tick) {
			matched = true
			u.capturedPoints(1)
		}
	}
	if !matched && u.gesturePoints < control.MaxExpressionPoints {
		if u.pendingShared == nil {
			u.pendingShared = map[uint64][]control.NoteExpression{}
		}
		if len(u.pendingShared) >= 512 && u.pendingShared[value.ID] == nil {
			u.recording = false
			u.status.Set("Shared input limit reached. Finish this take before recording more.")
			u.syncControls()
			return
		}
		points := u.pendingShared[value.ID]
		if len(points) > 0 {
			tick = max(tick, points[len(points)-1].Tick)
		}
		u.pendingShared[value.ID] = append(points, control.NoteExpression{Tick: tick, PitchCents: value.PitchCents, Pressure: value.Pressure, Timbre: value.Timbre})
		u.capturedPoints(1)
	}
}

func (u *liveUI) monitorShared(value midi.SharedControls) {
	for track := range u.monitorGroups[value.ID].Tracks {
		u.queue("live", map[string]string{"type": "group-expression", "track": track, "groupId": strconv.FormatUint(value.ID, 10), "pitchCents": strconv.FormatFloat(value.PitchCents, 'f', -1, 64), "pressure": strconv.FormatFloat(value.Pressure, 'f', -1, 64), "timbre": strconv.FormatFloat(value.Timbre, 'f', -1, 64)})
	}
}

func (u *liveUI) processMIDIAt(device string, data []byte, at *control.NoteTime) {
	notes, shared, err := u.midiNotes.ProcessEvents(device, data)
	if err != nil {
		u.status.Set(err.Error())
		return
	}
	for _, value := range shared {
		u.midiShared(value, at)
	}
	for _, note := range notes {
		u.midiNote(note, at)
	}
}
