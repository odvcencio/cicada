package main

import (
	"fmt"
	"net/http"
	"strings"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/seq"
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

type recordedStep struct {
	note, velocity int
	step           int
	slide          bool
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
	s.applyWithHook(w, edit, func(source []byte) ([]byte, error) {
		updated := source
		for _, recording := range edit.Recordings {
			var err error
			updated, err = recordedTakeSource(updated, recording.Track, recording.Pattern, recording.Notes)
			if err != nil {
				return nil, err
			}
		}
		committedRevision = studioRevision(updated)
		return updated, nil
	}, func() {
		if s.history != nil {
			s.history.expectRecordedTake(committedRevision)
		}
	})
}

func recordedTakeSource(source []byte, trackID, patternID string, take []studioTakeNote) ([]byte, error) {
	if trackID == "" || patternID == "" || len(take) == 0 {
		return nil, fmt.Errorf("track, pattern, and at least one recorded note are required")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before recording a take")
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must compile before recording a take")
	}
	var track *project.Track
	for index := range semantic.Tracks {
		if semantic.Tracks[index].ID == trackID {
			track = &semantic.Tracks[index]
			break
		}
	}
	if track == nil {
		return nil, fmt.Errorf("unknown track %q", trackID)
	}
	var pattern *project.Pattern
	for index := range semantic.Patterns {
		if semantic.Patterns[index].ID == patternID {
			pattern = &semantic.Patterns[index]
			break
		}
	}
	if pattern == nil {
		return nil, fmt.Errorf("unknown pattern %q", patternID)
	}
	usesPattern := false
	for _, slot := range track.Slots {
		if slot != nil && *slot == patternID {
			usesPattern = true
			break
		}
	}
	if !usesPattern {
		return nil, fmt.Errorf("pattern %q is not on track %q", patternID, trackID)
	}
	drumTrack := track.Kind == "drums"
	for _, kit := range semantic.Kits {
		if kit.ID == track.Kind {
			drumTrack = true
			break
		}
	}
	drums := pattern.Kind == "drums"
	if drums != drumTrack {
		return nil, fmt.Errorf("pattern %q does not match track %q", patternID, trackID)
	}
	pitched := track.Kind == "acid"
	for _, instrument := range semantic.Instruments {
		pitched = pitched || instrument.ID == track.Kind
	}
	if !drums && !pitched {
		return nil, fmt.Errorf("track %q is not a note track", trackID)
	}
	if pattern.Steps < 1 || pattern.Steps > 64 {
		return nil, fmt.Errorf("pattern %q has an invalid length", patternID)
	}

	steps := make(map[int]recordedStep)
	expressive := false
	for _, note := range take {
		if note.Tick < 0 || note.EndTick < note.Tick || note.Tick > 1<<60 || note.EndTick > 1<<60 {
			return nil, fmt.Errorf("recorded note time is out of range")
		}
		if note.Note < 0 || note.Note > 127 {
			return nil, fmt.Errorf("recorded note must be in MIDI range 0–127")
		}
		if note.Velocity < 1 || note.Velocity > 127 {
			return nil, fmt.Errorf("recorded velocity must be in MIDI range 1–127")
		}
		if note.Channel > 15 || note.NoteID == 65535 {
			return nil, fmt.Errorf("recorded note identity is out of range")
		}
		if err := validateTakeExpression(note); err != nil {
			return nil, err
		}
		expressive = expressive || len(note.Expressions) != 0
		step := int((note.Tick + seq.TicksPerStep/2) / seq.TicksPerStep % int64(pattern.Steps))
		key := step
		if drums {
			lane, ok := liveplay.GMDrumLane(note.Note)
			if !ok {
				return nil, fmt.Errorf("MIDI drum note %d is not in the General MIDI map", note.Note)
			}
			key += int(lane) * 64
		}
		steps[key] = recordedStep{note: note.Note, velocity: note.Velocity, step: step}
	}
	if expressive {
		if drums {
			return nil, fmt.Errorf("drum takes cannot contain per-note expression")
		}
		return recordedExpressionSource(source, score, pattern, take)
	}
	// Ordinary scalar takes keep their existing acid/drum contract. Custom
	// tracks may have chord steps whose pitch-grid edits toggle single pitches.
	if !drums && track.Kind != "acid" {
		return nil, fmt.Errorf("track %q is not an acid track", trackID)
	}
	if !drums {
		for _, prior := range take {
			priorStep := int((prior.Tick + seq.TicksPerStep/2) / seq.TicksPerStep % int64(pattern.Steps))
			for _, next := range take {
				if prior.Tick >= next.Tick || prior.EndTick <= next.Tick {
					continue
				}
				nextStep := int((next.Tick + seq.TicksPerStep/2) / seq.TicksPerStep % int64(pattern.Steps))
				if priorStep != nextStep {
					current := steps[priorStep]
					current.slide = true
					steps[priorStep] = current
				}
			}
		}
	}
	ordered := make([]recordedStep, 0, len(steps))
	for _, step := range steps {
		ordered = append(ordered, step)
	}
	for left := 0; left < len(ordered); left++ {
		for right := left + 1; right < len(ordered); right++ {
			if ordered[right].step < ordered[left].step {
				ordered[left], ordered[right] = ordered[right], ordered[left]
			}
		}
	}
	updated := append([]byte(nil), source...)
	for _, captured := range ordered {
		if drums {
			laneIndex, ok := liveplay.GMDrumLane(captured.note)
			if !ok {
				return nil, fmt.Errorf("MIDI drum note %d is not in the General MIDI map", captured.note)
			}
			lane := drumLaneName(laneIndex)
			token, err := studioDrumToken(updated, patternID, lane, captured.step)
			if err != nil {
				return nil, err
			}
			if token == "." || token == "-" {
				updated, err = toggledSource(updated, patternID, lane, captured.step)
				if err != nil {
					return nil, err
				}
			}
			updated, err = drumVelocitySource(updated, patternID, lane, captured.step, captured.velocity)
			if err != nil {
				return nil, err
			}
			continue
		}
		current, err := studioPatternStep(updated, patternID, captured.step)
		if err != nil {
			return nil, err
		}
		if current == nil || int(current.Note) != captured.note || current.Tie {
			updated, err = pitchedSource(updated, patternID, "", captured.step, captured.note)
			if err != nil {
				return nil, err
			}
		}
		if captured.slide {
			token, err := studioNoteToken(updated, patternID, captured.step)
			if err != nil {
				return nil, err
			}
			if token != "." && token != "-" && !strings.Contains(token, "~") {
				updated, err = toggledModifierSource(updated, patternID, "", captured.step, "slide")
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return updated, nil
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
