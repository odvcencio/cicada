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
		if event.PitchCents == 0 && event.Pressure == 0 && event.Timbre == .5 {
			return
		}
	case "note-off":
		u.noteOffAt(id, at)
		return
	}
	h, ok := u.held[id]
	if !ok {
		return
	}
	if u.props.ExpressionTracks[h.Track] {
		u.queue("live", map[string]string{"type": "note-expression", "track": h.Track, "noteId": strconv.Itoa(int(event.ID)), "pitchCents": strconv.FormatFloat(event.PitchCents, 'f', -1, 64), "pressure": strconv.FormatFloat(event.Pressure, 'f', -1, 64), "timbre": strconv.FormatFloat(event.Timbre, 'f', -1, 64)})
	} else {
		u.status.Set("This sound plays notes without gesture monitoring. Recorded gestures remain in the retained take; choose acid or an authored synth to apply them.")
	}
	if encoded, ok := u.active[id]; ok && u.gesturePoints < control.MaxExpressionPoints {
		i, n := encoded/1024, encoded%1024
		tick := u.tick()
		if at != nil {
			tick = at.Tick
		}
		u.takes[i].Notes[n].AddExpression(control.NoteExpression{Tick: tick, PitchCents: event.PitchCents, Pressure: event.Pressure, Timbre: event.Timbre})
		u.gesturePoints++
		u.takeDirty = true
		select {
		case u.checkpointWake <- struct{}{}:
		default:
		}
		if u.gesturePoints == control.MaxExpressionPoints {
			u.recording = false
			u.status.Set("65,536 gesture points retained. Finish this take before recording more.")
			u.syncControls()
		}
	}
}
