package main

import (
	"fmt"
	"net/http"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioTakeNote struct {
	Tick        int64                  `json:"tick"`
	EndTick     int64                  `json:"endTick"`
	Note        int                    `json:"note"`
	Velocity    int                    `json:"velocity"`
	NoteID      uint16                 `json:"noteId,omitempty"`
	Channel     uint8                  `json:"channel,omitempty"`
	Expressions []studioTakeExpression `json:"expressions,omitempty"`
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
	var committedRevision string
	intent := &edits.RecordTake{Recordings: editRecordings(edit.Recordings)}
	s.applyPreparedIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil, func(result *edits.Result) error {
		committedRevision = edits.Revision(result.Source)
		return nil
	}, func() {
		if s.history != nil {
			s.history.expectRecordedTake(committedRevision)
		}
	})
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

func studioPatternStep(source []byte, patternID string, index int) (*project.Step, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must compile before a grid edit")
	}
	for _, pattern := range semantic.Patterns {
		if pattern.ID == patternID {
			if pattern.Kind == "drums" || index < 0 || index >= len(pattern.Data) {
				return nil, fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
			}
			return pattern.Data[index], nil
		}
	}
	return nil, fmt.Errorf("unknown pattern %q", patternID)
}

func studioNoteToken(source []byte, patternID string, index int) (string, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return "", fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name == patternID && pattern.Kind != "drums" && index >= 0 && index < len(pattern.Steps) {
			return pattern.Steps[index].Text, nil
		}
	}
	return "", fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
}

func studioDrumToken(source []byte, patternID, laneID string, index int) (string, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return "", fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID || pattern.Kind != "drums" {
			continue
		}
		for _, lane := range pattern.Lanes {
			if lane.Name == laneID && index >= 0 && index < len(lane.Hits) {
				return lane.Hits[index].Text, nil
			}
		}
		return "", fmt.Errorf("pattern %s has no drum lane %s", patternID, laneID)
	}
	return "", fmt.Errorf("unknown drum pattern %q", patternID)
}

func drumVelocitySource(source []byte, patternID, laneID string, index, velocity int) ([]byte, error) {
	if velocity < 1 || velocity > 127 {
		return nil, fmt.Errorf("drum velocity must be in MIDI range 1–127")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID || pattern.Kind != "drums" {
			continue
		}
		for _, lane := range pattern.Lanes {
			if lane.Name != laneID || index < 0 || index >= len(lane.Hits) {
				continue
			}
			token := lane.Hits[index]
			if token.Text == "." || token.Text == "-" || token.Text == "" || token.Text[0] != 'x' && token.Text[0] != 'X' {
				return nil, fmt.Errorf("drum step %d needs a hit before setting velocity", index+1)
			}
			base := "x"
			bestDistance := intAbs(capturedVelocity("x") - velocity)
			for level := 1; level <= 9; level++ {
				candidate := fmt.Sprintf("x%d", level)
				distance := intAbs(level*14 - velocity)
				if distance < bestDistance {
					base, bestDistance = candidate, distance
				}
			}
			distance := intAbs(127 - velocity)
			if distance < bestDistance {
				base = "X"
			}
			suffixAt := strings.IndexAny(token.Text[1:], "*?%")
			suffix := ""
			if suffixAt >= 0 {
				suffix = token.Text[1+suffixAt:]
			}
			replacement := base + suffix
			start := studioSourceOffset(source, token.Position)
			end := start + len(token.Text)
			if end > len(source) || string(source[start:end]) != token.Text {
				return nil, fmt.Errorf("step source no longer matches the projection")
			}
			updated := make([]byte, 0, len(source)-len(token.Text)+len(replacement))
			updated = append(updated, source[:start]...)
			updated = append(updated, replacement...)
			updated = append(updated, source[end:]...)
			return updated, nil
		}
		return nil, fmt.Errorf("pattern %s has no drum lane %s", patternID, laneID)
	}
	return nil, fmt.Errorf("unknown drum pattern %q", patternID)
}

func intAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func capturedVelocity(base string) int {
	if base == "x" {
		return 100
	}
	return 127
}

func drumLaneName(index uint16) string {
	if int(index) >= len(drumLaneOrder) {
		return ""
	}
	return drumLaneOrder[index]
}
