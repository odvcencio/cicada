package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func validateTakeExpression(note studioTakeNote) error {
	if len(note.Expressions) > 65536 {
		return fmt.Errorf("recorded note has too many expression samples")
	}
	previous := note.Tick
	for _, value := range note.Expressions {
		if value.Tick < previous || value.Tick > note.EndTick {
			return fmt.Errorf("recorded expression samples must be ordered within their note")
		}
		previous = value.Tick
		if value.VibratoDepthCents != nil && (math.IsNaN(*value.VibratoDepthCents) || math.IsInf(*value.VibratoDepthCents, 0) || *value.VibratoDepthCents < 0 || *value.VibratoDepthCents > 9600) {
			return fmt.Errorf("recorded vibrato depth must be finite cents from 0 to 9600")
		}
		for _, field := range []struct{ value, minimum, maximum float64 }{
			{value.PitchCents, -9600, 9600}, {value.Pressure, 0, 1}, {value.Timbre, 0, 1},
		} {
			if math.IsNaN(field.value) || math.IsInf(field.value, 0) || field.value < field.minimum || field.value > field.maximum {
				return fmt.Errorf("recorded expression needs finite pitch cents -9600–9600 and pressure/timbre 0–1")
			}
		}
	}
	return nil
}

// A melodic pattern has one voice per step. Refuse overlapping expressive
// voices rather than silently replacing their pitches or expression curves.
func recordedExpressionSource(source []byte, score *notation.Score, pattern *project.Pattern, take []studioTakeNote) ([]byte, error) {
	var authored *notation.Pattern
	for i := range score.Patterns {
		if score.Patterns[i].Name == pattern.ID {
			authored = &score.Patterns[i]
			break
		}
	}
	if authored == nil {
		return nil, fmt.Errorf("unknown pattern %q", pattern.ID)
	}
	for _, step := range pattern.Data {
		if step != nil && len(step.Notes) != 0 {
			return nil, fmt.Errorf("record expression into a note pattern without chord steps; the take has not been committed")
		}
	}
	for _, part := range authored.Parts {
		if part.Use != nil {
			return nil, fmt.Errorf("record expression into a pattern with directly authored note steps")
		}
	}
	for i, note := range take {
		for _, other := range take[i+1:] {
			if note.Tick < other.EndTick && other.Tick < note.EndTick {
				return nil, fmt.Errorf("overlapping expressive notes need separate note patterns; the take has not been committed")
			}
		}
	}
	values := make([]project.NoteExpression, pattern.Steps)
	for i := range values {
		values[i].Timbre = .5
	}
	copy(values, pattern.Expression)
	occupied := make([]bool, pattern.Steps)
	type sourceEdit struct {
		start, end int
		text       string
	}
	edits := make([]sourceEdit, 0, len(take)+4)
	quantize := func(tick int64) int64 { return (tick + seq.TicksPerStep/2) / seq.TicksPerStep }
	for _, note := range take {
		start, end := quantize(note.Tick), quantize(note.EndTick)
		if end <= start {
			end = start + 1
		}
		if end-start > int64(pattern.Steps) {
			return nil, fmt.Errorf("an expressive note cannot span more than one pattern loop")
		}
		value := project.NoteExpression{Timbre: .5}
		sample := 0
		for step := start; step < end; step++ {
			index := int(step % int64(pattern.Steps))
			if occupied[index] {
				return nil, fmt.Errorf("expressive notes quantize to the same pattern step; the take has not been committed")
			}
			occupied[index] = true
			firstSample := sample
			for sample < len(note.Expressions) && quantize(note.Expressions[sample].Tick) <= step {
				captured := note.Expressions[sample]
				value = project.NoteExpression{PitchCents: float32(captured.PitchCents), Pressure: float32(captured.Pressure), Timbre: float32(captured.Timbre)}
				if captured.VibratoDepthCents != nil {
					value.VibratoDepthCents = float32(*captured.VibratoDepthCents)
				}
				sample++
			}
			// A release-near sample rounds past the last held step; retain its
			// value on that last step rather than writing onto the next note.
			if step == end-1 && sample < len(note.Expressions) {
				captured := note.Expressions[len(note.Expressions)-1]
				value = project.NoteExpression{PitchCents: float32(captured.PitchCents), Pressure: float32(captured.Pressure), Timbre: float32(captured.Timbre)}
				if captured.VibratoDepthCents != nil {
					value.VibratoDepthCents = float32(*captured.VibratoDepthCents)
				}
				sample = len(note.Expressions)
			}
			if firstSample < sample && note.Expressions[sample-1].VibratoDepthCents == nil {
				if center, depth, ok := takeVibrato(note.Expressions[firstSample:sample]); ok {
					value.PitchCents, value.VibratoDepthCents = center, depth
				}
			}
			values[index] = value
			token := authored.Steps[index]
			at := studioSourceOffset(source, token.Position)
			text := "-"
			if step == start {
				text = sourcePitch(note.Note)
			}
			edits = append(edits, sourceEdit{at, at + len(token.Text), text})
		}
	}
	for i := range occupied {
		next := (i + 1) % len(occupied)
		if occupied[i] && !occupied[next] {
			values[next] = project.NoteExpression{Timbre: .5}
		}
	}
	rows := []struct {
		name, unit string
		value      func(project.NoteExpression) float32
	}{
		{"bend", "ct", func(value project.NoteExpression) float32 { return value.PitchCents }},
		{"vibrato", "ct", func(value project.NoteExpression) float32 { return value.VibratoDepthCents }},
		{"pressure", "", func(value project.NoteExpression) float32 { return value.Pressure }},
		{"timbre", "", func(value project.NoteExpression) float32 { return value.Timbre }},
	}
	var additions strings.Builder
	for _, row := range rows {
		cells := make([]string, len(values))
		for i, value := range values {
			cells[i] = strconv.FormatFloat(float64(row.value(value)), 'f', -1, 32) + row.unit
		}
		text := row.name + ": " + strings.Join(cells, " ")
		found := false
		for _, prior := range authored.Expression {
			if prior.Name != row.name || len(prior.Values) == 0 {
				continue
			}
			for i, token := range prior.Values {
				at := studioSourceOffset(source, token.Position)
				edits = append(edits, sourceEdit{at, at + len(token.Text), cells[i]})
			}
			found = true
		}
		if !found {
			additions.WriteString("\n  " + text)
		}
	}
	if additions.Len() > 0 {
		// Directly authored melodic patterns contain no nested braces. The
		// last step or existing expression row identifies the closing brace.
		last := authored.Steps[len(authored.Steps)-1]
		end := studioSourceOffset(source, last.Position) + len(last.Text)
		for _, row := range authored.Expression {
			if len(row.Values) == 0 {
				continue
			}
			token := row.Values[len(row.Values)-1]
			end = max(end, studioSourceOffset(source, token.Position)+len(token.Text))
		}
		closing := takeClosingBrace(source[end:])
		if closing < 0 {
			return nil, fmt.Errorf("pattern closing brace is missing")
		}
		at := end + closing
		edits = append(edits, sourceEdit{at, at, additions.String() + "\n"})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	updated := append([]byte(nil), source...)
	for _, edit := range edits {
		if edit.start < 0 || edit.end > len(updated) || edit.start > edit.end {
			return nil, fmt.Errorf("recorded expression source no longer matches the score")
		}
		replacement := make([]byte, 0, len(updated)-edit.end+edit.start+len(edit.text))
		replacement = append(replacement, updated[:edit.start]...)
		replacement = append(replacement, edit.text...)
		replacement = append(replacement, updated[edit.end:]...)
		updated = replacement
	}
	return updated, nil
}

func takeClosingBrace(source []byte) int {
	for i := 0; i < len(source); i++ {
		if source[i] == '/' && i+1 < len(source) && source[i+1] == '/' {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			continue
		}
		if source[i] == '}' {
			return i
		}
	}
	return -1
}

// MPE carries the pitch curve rather than a separate vibrato controller. Two
// reversals distinguish an oscillation from a one-way bend. The score preserves
// its center and depth at the score's fixed 5 Hz rate, sampled per grid step.
func takeVibrato(samples []studioTakeExpression) (center, depth float32, ok bool) {
	if len(samples) < 4 {
		return 0, 0, false
	}
	minimum, maximum := samples[0].PitchCents, samples[0].PitchCents
	direction, reversals := 0, 0
	for i, value := range samples {
		minimum, maximum = min(minimum, value.PitchCents), max(maximum, value.PitchCents)
		if i == 0 {
			continue
		}
		delta := value.PitchCents - samples[i-1].PitchCents
		if math.Abs(delta) < .01 {
			continue
		}
		next := 1
		if delta < 0 {
			next = -1
		}
		if direction != 0 && direction != next {
			reversals++
		}
		direction = next
	}
	if reversals < 2 || maximum-minimum < 1 {
		return 0, 0, false
	}
	return float32((minimum + maximum) / 2), float32((maximum - minimum) / 2), true
}
