package edit

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/notation"
)

// RecordTake commits all recorded patterns as one validated edit.
type RecordTake struct {
	Recordings []Recording `json:"recordings"`
}

func (RecordTake) Kind() string { return "recordtake" }
func init() {
	Register("recordtake", func() Intent { return &RecordTake{} })
	Handle("recordtake", func(ctx *Context, intent Intent) error {
		in := intent.(*RecordTake)
		if len(in.Recordings) == 0 {
			return fmt.Errorf("at least one recorded track and pattern are required")
		}
		count := 0
		for _, recording := range in.Recordings {
			updated, err := recordedTakeSource(ctx, recording.Track, recording.Pattern, recording.Notes)
			if err != nil {
				return err
			}
			ctx.Source, ctx.plan = updated, nil
			count += len(recording.Notes)
		}
		label := fmt.Sprintf("Take committed · %d notes across %d patterns", count, len(in.Recordings))
		if len(in.Recordings) == 1 {
			label = fmt.Sprintf("Take committed · %s · %d notes", in.Recordings[0].Pattern, count)
		}
		ctx.SetLabel(label)
		return nil
	})
}

type TakeNote struct {
	Tick        int64            `json:"tick"`
	EndTick     int64            `json:"endtick"`
	Note        int              `json:"note"`
	Velocity    int              `json:"velocity"`
	NoteID      uint16           `json:"noteid,omitempty"`
	Channel     uint8            `json:"channel,omitempty"`
	Expressions []TakeExpression `json:"expressions,omitempty"`
}

type TakeExpression struct {
	Tick              int64    `json:"tick"`
	PitchCents        float64  `json:"pitchcents"`
	Pressure          float64  `json:"pressure"`
	Timbre            float64  `json:"timbre"`
	VibratoDepthCents *float64 `json:"vibratodepthcents,omitempty"`
}

type Recording struct {
	Track   string     `json:"track"`
	Pattern string     `json:"pattern"`
	Notes   []TakeNote `json:"notes"`
}

type recordedStep struct {
	note, velocity int
	step           int
	slide          bool
}

func recordedTakeSource(ctx *Context, trackID, patternID string, take []TakeNote) ([]byte, error) {
	if trackID == "" || patternID == "" || len(take) == 0 {
		return nil, fmt.Errorf("track, pattern, and at least one recorded note are required")
	}
	source := ctx.Source
	score, diagnostics, err := ctx.ParseProject()
	if err != nil {
		return nil, err
	}
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before recording a take")
	}
	semantic, err := ctx.CurrentPlan()
	if semantic == nil || err != nil {
		return nil, fmt.Errorf("score must compile before recording a take")
	}
	var track *Track
	for index := range semantic.Tracks {
		if semantic.Tracks[index].ID == trackID {
			track = &semantic.Tracks[index]
			break
		}
	}
	if track == nil {
		return nil, fmt.Errorf("unknown track %q", trackID)
	}
	var pattern *Pattern
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
	modeledKeys := keyboard.ID(track.Kind) != 0
	for _, kit := range score.Kits {
		if kit.Name == track.Kind {
			drumTrack = true
			modeledKeys = false
			break
		}
	}
	for _, sampler := range score.Samplers {
		if sampler.Name == track.Kind {
			modeledKeys = false
			break
		}
	}
	drums := pattern.Kind == "drums"
	if drums != drumTrack {
		return nil, fmt.Errorf("pattern %q does not match track %q", patternID, trackID)
	}
	pitched, polyphonic := track.Kind == "acid" || modeledKeys, modeledKeys
	for _, voice := range score.Instruments {
		if voice.Name == track.Kind {
			pitched, polyphonic = true, voice.Mode == "poly"
			modeledKeys = false
			break
		}
	}
	if !drums && !pitched {
		return nil, fmt.Errorf("track %q is not a pitched instrument track", trackID)
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
		if modeledKeys && (note.Note < keyboard.MinNote || note.Note > keyboard.MaxNote || note.Note+int(pattern.Transpose) < keyboard.MinNote || note.Note+int(pattern.Transpose) > keyboard.MaxNote) {
			return nil, fmt.Errorf("recorded keyboard note must stay in MIDI range 21–108 after pattern transposition")
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
			lane, ok := GMDrumLane(note.Note)
			if !ok {
				return nil, fmt.Errorf("MIDI drum note %d is not in the General MIDI map", note.Note)
			}
			key += int(lane) * 64
		}
		steps[key] = recordedStep{note: note.Note, velocity: note.Velocity, step: step}
	}
	if expressive {
		if modeledKeys {
			return nil, fmt.Errorf("CICADA-UNSUPPORTED: modeled keyboard track %q cannot record per-note expression", trackID)
		}
		if drums {
			return nil, fmt.Errorf("drum takes cannot contain per-note expression")
		}
		return recordedExpressionSource(source, ctx.Options.Path, score, pattern, take)
	}
	if polyphonic && track.Polyphony == 4 {
		return nil, fmt.Errorf("track %q is not an acid track", trackID)
	}
	if polyphonic {
		// Existing notes patterns hold one pitch per step. Refuse a chord take
		// before editing any steps, preserving every retained performance note.
		for i, note := range take {
			for _, other := range take[i+1:] {
				if note.Tick < other.EndTick && other.Tick < note.EndTick {
					return nil, fmt.Errorf("notes patterns hold one pitch per step; record a single-note take for polyphonic track %q", trackID)
				}
			}
		}
	}
	if !drums && !polyphonic {
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
			laneIndex, ok := GMDrumLane(captured.note)
			if !ok {
				return nil, fmt.Errorf("MIDI drum note %d is not in the General MIDI map", captured.note)
			}
			lane := drumLaneName(laneIndex)
			token, err := drumToken(sourceContext(ctx, updated), patternID, lane, captured.step)
			if err != nil {
				return nil, err
			}
			if token == "." || token == "-" {
				updated, err = toggledSource(sourceContext(ctx, updated), patternID, lane, captured.step)
				if err != nil {
					return nil, err
				}
			}
			updated, err = drumVelocitySource(sourceContext(ctx, updated), patternID, lane, captured.step, captured.velocity)
			if err != nil {
				return nil, err
			}
			continue
		}
		current, err := patternStep(sourceContext(ctx, updated), patternID, captured.step)
		if err != nil {
			return nil, err
		}
		if current == nil || int(current.Note) != captured.note || current.Tie {
			updated, err = pitchedSource(sourceContext(ctx, updated), patternID, "", captured.step, captured.note)
			if err != nil {
				return nil, err
			}
		}
		if captured.slide {
			token, err := noteToken(sourceContext(ctx, updated), patternID, captured.step)
			if err != nil {
				return nil, err
			}
			if token != "." && token != "-" && !strings.Contains(token, "~") {
				updated, err = toggledModifierSource(sourceContext(ctx, updated), patternID, "", captured.step, "slide")
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return updated, nil
}

func noteToken(ctx *Context, patternID string, index int) (string, error) {
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
		return "", fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name == patternID && pattern.Kind != "drums" && index >= 0 && index < len(pattern.Steps) {
			return pattern.Steps[index].Text, nil
		}
	}
	return "", fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
}

func drumToken(ctx *Context, patternID, laneID string, index int) (string, error) {
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
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

func validateTakeExpression(note TakeNote) error {
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
func recordedExpressionSource(source []byte, targetFile string, score *notation.Score, pattern *Pattern, take []TakeNote) ([]byte, error) {
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
	if _, err := Offset(source, targetFile, authored.Position); err != nil {
		return nil, fmt.Errorf("cannot record expression into pattern %q: %w", pattern.ID, err)
	}
	for _, token := range authored.Steps {
		if _, err := Offset(source, targetFile, token.Position); err != nil {
			return nil, fmt.Errorf("cannot record expression into pattern %q: %w", pattern.ID, err)
		}
	}
	for _, row := range authored.Expression {
		for _, token := range row.Values {
			if _, err := Offset(source, targetFile, token.Position); err != nil {
				return nil, fmt.Errorf("cannot record expression into pattern %q: %w", pattern.ID, err)
			}
		}
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
	values := make([]NoteExpression, pattern.Steps)
	for i := range values {
		values[i].Timbre = .5
	}
	copy(values, pattern.Expression)
	occupied := make([]bool, pattern.Steps)
	edits := make([]Span, 0, len(take)+4)
	quantize := func(tick int64) int64 { return (tick + seq.TicksPerStep/2) / seq.TicksPerStep }
	for _, note := range take {
		start, end := quantize(note.Tick), quantize(note.EndTick)
		if end <= start {
			end = start + 1
		}
		if end-start > int64(pattern.Steps) {
			return nil, fmt.Errorf("an expressive note cannot span more than one pattern loop")
		}
		value := NoteExpression{Timbre: .5}
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
				value = NoteExpression{PitchCents: float32(captured.PitchCents), Pressure: float32(captured.Pressure), Timbre: float32(captured.Timbre)}
				if captured.VibratoDepthCents != nil {
					value.VibratoDepthCents = float32(*captured.VibratoDepthCents)
				}
				sample++
			}
			// A release-near sample rounds past the last held step; retain its
			// value on that last step rather than writing onto the next note.
			if step == end-1 && sample < len(note.Expressions) {
				captured := note.Expressions[len(note.Expressions)-1]
				value = NoteExpression{PitchCents: float32(captured.PitchCents), Pressure: float32(captured.Pressure), Timbre: float32(captured.Timbre)}
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
			at, err := Offset(source, targetFile, token.Position)
			if err != nil {
				return nil, err
			}
			text := "-"
			if step == start {
				text = sourcePitch(note.Note)
			}
			edits = append(edits, Span{at, at + len(token.Text), text, targetFile})
		}
	}
	for i := range occupied {
		next := (i + 1) % len(occupied)
		if occupied[i] && !occupied[next] {
			values[next] = NoteExpression{Timbre: .5}
		}
	}
	rows := []struct {
		name, unit string
		value      func(NoteExpression) float32
	}{
		{"bend", "ct", func(value NoteExpression) float32 { return value.PitchCents }},
		{"vibrato", "ct", func(value NoteExpression) float32 { return value.VibratoDepthCents }},
		{"pressure", "", func(value NoteExpression) float32 { return value.Pressure }},
		{"timbre", "", func(value NoteExpression) float32 { return value.Timbre }},
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
				at, err := Offset(source, targetFile, token.Position)
				if err != nil {
					return nil, err
				}
				edits = append(edits, Span{at, at + len(token.Text), cells[i], targetFile})
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
		end, err := Offset(source, targetFile, last.Position)
		if err != nil {
			return nil, err
		}
		end += len(last.Text)
		for _, row := range authored.Expression {
			if len(row.Values) == 0 {
				continue
			}
			token := row.Values[len(row.Values)-1]
			at, err := Offset(source, targetFile, token.Position)
			if err != nil {
				return nil, err
			}
			end = max(end, at+len(token.Text))
		}
		closing := takeClosingBrace(source[end:])
		if closing < 0 {
			return nil, fmt.Errorf("pattern closing brace is missing")
		}
		at := end + closing
		edits = append(edits, Span{at, at, additions.String() + "\n", targetFile})
	}
	return PatchSpans(source, targetFile, edits)
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
func takeVibrato(samples []TakeExpression) (center, depth float32, ok bool) {
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
