package main

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var sourcePitchNames = [...]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}

// pitchedSource toggles a grid pitch in an authored step token.
// Chords add/remove only the clicked pitch; scalar notes retain set/clear
// behavior. Phrase expansions point back to their shared source token, so a
// grid edit updates every use of that phrase.
func pitchedSource(source []byte, patternID, laneID string, index, pitch int) ([]byte, error) {
	if patternID == "" || laneID != "" || index < 0 || pitch < 0 || pitch > 127 {
		return nil, fmt.Errorf("a note pattern, nonnegative step, and MIDI pitch 0–127 are required")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must compile before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID {
			continue
		}
		if pattern.Kind == "drums" || index >= len(pattern.Steps) {
			return nil, fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
		}
		token := pattern.Steps[index]
		start := studioSourceOffset(source, token.Position)
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		var current *project.Step
		for _, candidate := range semantic.Patterns {
			if candidate.ID == patternID && index < len(candidate.Data) {
				current = candidate.Data[index]
				break
			}
		}
		replacement := "."
		if current != nil && len(current.Notes) > 0 {
			var err error
			replacement, err = toggledChordPitch(token, current.Notes, pitch)
			if err != nil {
				return nil, err
			}
		} else if current == nil || current.Tie || int(current.Note) != pitch {
			sourceNote := pitch - token.Transpose
			if sourceNote < 0 || sourceNote > 127 {
				return nil, fmt.Errorf("pitch %d cannot be written through phrase transpose %+d", pitch, token.Transpose)
			}
			replacement = sourcePitch(sourceNote)
			if token.Text != "." && token.Text != "-" {
				if suffix := strings.IndexAny(token.Text, "^~*?%"); suffix >= 0 {
					replacement += token.Text[suffix:]
				}
			}
		}
		updated := make([]byte, 0, len(source)-len(token.Text)+len(replacement))
		updated = append(updated, source[:start]...)
		updated = append(updated, replacement...)
		updated = append(updated, source[end:]...)
		return updated, nil
	}
	return nil, fmt.Errorf("unknown pattern %q", patternID)
}

// toggledChordPitch retains untouched source spellings and spacing, rather than
// respelling a chord through its compiled MIDI pitches. notes is in authored
// order, including any phrase-use transpose.
func toggledChordPitch(token notation.StepToken, notes []int, pitch int) (string, error) {
	close := strings.IndexByte(token.Text, ']')
	if close < 0 || !strings.HasPrefix(token.Text, "[") {
		return "", fmt.Errorf("chord source no longer matches the projection")
	}
	pitches := token.ChordPitches
	if len(pitches) != len(notes) || len(notes) < 2 || len(notes) > 4 {
		return "", fmt.Errorf("chord source no longer matches the projection")
	}
	starts, ends := make([]int, len(pitches)), make([]int, len(pitches))
	remove := -1
	for i, spelling := range pitches {
		starts[i], ends[i] = spelling.Start, spelling.End
		if notes[i] == pitch {
			remove = i
		}
	}
	if remove >= 0 {
		if len(pitches) == 2 {
			// One pitch is scalar again. Unlike chord suffixes, scalar modifiers
			// must be adjacent; preserve their values and the surviving spelling.
			prefix := ""
			if len(token.ChordComments) > 0 {
				prefix = strings.Join(token.ChordComments, "\n") + "\n"
			}
			return prefix + pitches[1-remove].Text + token.ChordModifiers, nil
		}
		start, end := starts[remove], ends[remove]
		if remove+1 < len(pitches) {
			if strings.TrimSpace(token.Text[end:starts[remove+1]]) == "" {
				end = starts[remove+1]
			}
		} else {
			if strings.TrimSpace(token.Text[ends[remove-1]:start]) == "" {
				start = ends[remove-1]
			}
		}
		return token.Text[:start] + token.Text[end:], nil
	}
	if len(pitches) == 4 {
		return "", fmt.Errorf("chord already has 4 pitches; remove a pitch before adding another")
	}
	sourceNote := pitch - token.Transpose
	if sourceNote < 0 || sourceNote > 127 {
		return "", fmt.Errorf("pitch %d cannot be written through phrase transpose %+d", pitch, token.Transpose)
	}
	at := ends[len(pitches)-1]
	return token.Text[:at] + " " + sourcePitch(sourceNote) + token.Text[at:], nil
}

func sourcePitch(pitch int) string {
	octave := pitch/12 - 1
	base := min(6, max(0, octave))
	spelling := sourcePitchNames[pitch%12] + strconv.Itoa(base)
	if octave < 0 {
		return spelling + strings.Repeat(",", -octave)
	}
	return spelling + strings.Repeat("'", octave-base)
}
