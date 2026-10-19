package main

import (
	"net/http"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/midi"
)

type studioTakeNote struct {
	Tick         int64                  `json:"tick"`
	EndTick      int64                  `json:"endTick"`
	Note         int                    `json:"note"`
	Velocity     int                    `json:"velocity"`
	NoteID       uint16                 `json:"noteId,omitempty"`
	Channel      uint8                  `json:"channel,omitempty"`
	Expressions  []studioTakeExpression `json:"expressions,omitempty"`
	Input        *midi.NoteTiming       `json:"input,omitempty"`
	EndEstimated bool                   `json:"endEstimated,omitempty"`
}

type studioTakeExpression struct {
	Tick              int64    `json:"tick"`
	PitchCents        float64  `json:"pitchCents"`
	Pressure          float64  `json:"pressure"`
	Timbre            float64  `json:"timbre"`
	VibratoDepthCents *float64 `json:"vibratoDepthCents,omitempty"`
}

type studioTakeRecording struct {
	Track   string           `json:"track"`
	Pattern string           `json:"pattern"`
	Notes   []studioTakeNote `json:"notes"`
}

func (s *studio) recordTake(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if len(edit.Recordings) == 0 && edit.Track != "" && edit.Pattern != "" && len(edit.Take) != 0 {
		edit.Recordings = []studioTakeRecording{{Track: edit.Track, Pattern: edit.Pattern, Notes: edit.Take}}
	}
	if len(edit.Recordings) == 0 {
		studioJSON(w, 400, map[string]string{"error": "at least one recorded track and pattern are required"})
		return
	}
	edit.Action, edit.Count = "record", 0
	edit.Pattern = ""
	edit.PatternCount = len(edit.Recordings)
	for _, recording := range edit.Recordings {
		if recording.Track == "" || recording.Pattern == "" || len(recording.Notes) == 0 {
			studioJSON(w, 400, map[string]string{"error": "each take needs a track, pattern, and at least one note"})
			return
		}
		edit.Count += len(recording.Notes)
		if len(edit.Recordings) == 1 {
			edit.Pattern = recording.Pattern
		}
	}
	// The commit tail arms the poll handoff once the write lands, keyed by the
	// playback fingerprint that poll will compute (entry, every source file and
	// cicada.mod), so an unrelated edit to another file cannot consume it.
	edit.recordedTake = true
	intent := &edits.RecordTake{Recordings: editRecordings(edit.Recordings)}
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil)
}

func editRecordings(recordings []studioTakeRecording) []edits.Recording {
	result := make([]edits.Recording, len(recordings))
	for i, recording := range recordings {
		result[i] = edits.Recording{Track: recording.Track, Pattern: recording.Pattern}
		for _, note := range recording.Notes {
			out := edits.TakeNote{Tick: note.Tick, EndTick: note.EndTick, Note: note.Note, Velocity: note.Velocity, NoteID: note.NoteID, Channel: note.Channel}
			for _, value := range note.Expressions {
				out.Expressions = append(out.Expressions, edits.TakeExpression{Tick: value.Tick, PitchCents: value.PitchCents, Pressure: value.Pressure, Timbre: value.Timbre, VibratoDepthCents: value.VibratoDepthCents})
			}
			result[i].Notes = append(result[i].Notes, out)
		}
	}
	return result
}
