package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

// ToggleStep changes one grid token. JSON shared accepts:
// "" (default): ask when the step comes from a shared phrase;
// "definition": edit the phrase token;
// "detach": expand only the use containing the step;
// "pattern": expand every use in the pattern before editing.
// Directly authored steps ignore "" and "detach"; "pattern" still expands
// every use. The same shared schema applies to the other grid intents.
type ToggleStep struct {
	Entity EntityID `json:"entity"`
	Shared string   `json:"shared,omitempty"`
}

func (ToggleStep) Kind() string { return "togglestep" }

// SetPitch's JSON pitch is required and accepts MIDI 0–127. An omitted or
// null pitch is refused. Shared uses ToggleStep's JSON schema.
type SetPitch struct {
	Entity       EntityID `json:"entity"`
	Pitch        int      `json:"pitch"`
	Shared       string   `json:"shared,omitempty"`
	missingPitch bool
}

func (SetPitch) Kind() string { return "setpitch" }

func (in SetPitch) MarshalJSON() ([]byte, error) {
	if in.missingPitch {
		return json.Marshal(struct {
			Entity EntityID `json:"entity"`
			Shared string   `json:"shared,omitempty"`
		}{in.Entity, in.Shared})
	}
	type wire SetPitch
	return json.Marshal(wire(in))
}

func (in *SetPitch) UnmarshalJSON(data []byte) error {
	type wire SetPitch
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var required struct {
		Pitch *int `json:"pitch"`
	}
	if err := json.Unmarshal(data, &required); err != nil {
		return err
	}
	*in = SetPitch(decoded)
	in.missingPitch = required.Pitch == nil
	return nil
}

type ToggleModifier struct {
	Entity   EntityID `json:"entity"`
	Modifier string   `json:"modifier"`
	Shared   string   `json:"shared,omitempty"`
}

func (ToggleModifier) Kind() string { return "togglemodifier" }

type CycleStep struct {
	Entity EntityID `json:"entity"`
	Field  string   `json:"field"`
	Shared string   `json:"shared,omitempty"`
}

func (CycleStep) Kind() string { return "cyclestep" }

type SetDrumVelocity struct {
	Entity   EntityID `json:"entity"`
	Velocity int      `json:"velocity"`
	Shared   string   `json:"shared,omitempty"`
}

func (SetDrumVelocity) Kind() string { return "setdrumvelocity" }

func init() {
	Register("togglestep", func() Intent { return &ToggleStep{} })
	Register("setpitch", func() Intent { return &SetPitch{} })
	Register("togglemodifier", func() Intent { return &ToggleModifier{} })
	Register("cyclestep", func() Intent { return &CycleStep{} })
	Register("setdrumvelocity", func() Intent { return &SetDrumVelocity{} })
	for _, kind := range []string{"togglestep", "setpitch", "togglemodifier", "cyclestep", "setdrumvelocity"} {
		Handle(kind, editGrid)
	}
}

func stepTarget(id EntityID) (pattern, lane string, index int, err error) {
	if _, err = ParseEntityID(string(id)); err != nil {
		return
	}
	if id.Kind() != KindStep {
		err = fmt.Errorf("grid edit needs a step entity, got %q", id)
		return
	}
	parts := id.segments()
	pattern = parts[0]
	if len(parts) == 3 {
		lane = parts[1]
	}
	index, err = strconv.Atoi(parts[len(parts)-1])
	return
}

func editGrid(ctx *Context, intent Intent) error {
	var entity EntityID
	var shared string
	switch in := intent.(type) {
	case *ToggleStep:
		entity, shared = in.Entity, in.Shared
	case *SetPitch:
		entity, shared = in.Entity, in.Shared
	case *ToggleModifier:
		entity, shared = in.Entity, in.Shared
	case *CycleStep:
		entity, shared = in.Entity, in.Shared
	case *SetDrumVelocity:
		entity, shared = in.Entity, in.Shared
	}
	pattern, lane, index, err := stepTarget(entity)
	if err != nil {
		return err
	}
	source, err := resolveShared(ctx, entity, shared)
	if err != nil {
		return err
	}
	if !bytes.Equal(source, ctx.Source) {
		ctx.Source, ctx.plan = source, nil
	}
	var updated []byte
	what := "toggled"
	switch in := intent.(type) {
	case *ToggleStep:
		updated, err = toggledSource(ctx, pattern, lane, index)
	case *SetPitch:
		if in.missingPitch {
			return fmt.Errorf("choose a pitch")
		}
		updated, err = pitchedSource(ctx, pattern, lane, index, in.Pitch)
		what = "pitch changed"
	case *ToggleModifier:
		updated, err = toggledModifierSource(ctx, pattern, lane, index, in.Modifier)
		what = in.Modifier + " changed"
	case *CycleStep:
		updated, err = cycledStepSource(ctx, pattern, lane, index, in.Field)
		what = in.Field + " changed"
	case *SetDrumVelocity:
		updated, err = drumVelocitySource(ctx, pattern, lane, index, in.Velocity)
		what = "velocity changed"
	}
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	if lane != "" {
		ctx.SetLabel(fmt.Sprintf("Grid · %s / %s step %d %s", pattern, lane, index+1, what))
	} else {
		ctx.SetLabel(fmt.Sprintf("Grid · %s step %d %s", pattern, index+1, what))
	}
	return nil
}

func toggledSource(ctx *Context, patternID, laneID string, index int) ([]byte, error) {
	source := ctx.Source
	if index < 0 || patternID == "" {
		return nil, fmt.Errorf("pattern and nonnegative step are required")
	}
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID {
			continue
		}
		var token notation.StepToken
		isDrum := pattern.Kind == "drums"
		if isDrum {
			for _, lane := range pattern.Lanes {
				if lane.Name == laneID && index < len(lane.Hits) {
					token = lane.Hits[index]
					break
				}
			}
		} else if laneID == "" && index < len(pattern.Steps) {
			token = pattern.Steps[index]
		}
		if token.Position.Line == 0 {
			return nil, fmt.Errorf("pattern %s has no step %d in lane %s", patternID, index+1, laneID)
		}
		start, err := Offset(source, ctx.Options.Path, token.Position)
		if err != nil {
			return nil, err
		}
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		replacement := "."
		if token.Text == "." {
			if isDrum {
				replacement = "x"
			} else {
				replacement = "1"
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

var sourcePitchNames = [...]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}

// pitchedSource toggles a grid pitch in an authored step token.
// Chords add/remove only the clicked pitch; scalar notes retain set/clear
// behavior. Phrase expansions point back to their shared source token, so a
// grid edit updates every use of that phrase.
func pitchedSource(ctx *Context, patternID, laneID string, index, pitch int) ([]byte, error) {
	source := ctx.Source
	if patternID == "" || laneID != "" || index < 0 || pitch < 0 || pitch > 127 {
		return nil, fmt.Errorf("a note pattern, nonnegative step, and MIDI pitch 0–127 are required")
	}
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	semantic, err := ctx.CurrentPlan()
	if semantic == nil || err != nil {
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
		start, err := Offset(source, ctx.Options.Path, token.Position)
		if err != nil {
			return nil, err
		}
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		var current *Step
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

// toggledModifierSource changes the authored note token, including when that
// token belongs to a phrase used by multiple pattern steps.
func toggledModifierSource(ctx *Context, patternID, laneID string, index int, modifier string) ([]byte, error) {
	source := ctx.Source
	if patternID == "" || laneID != "" || index < 0 || modifier != "accent" && modifier != "slide" {
		return nil, fmt.Errorf("a note pattern, nonnegative step, and accent or slide are required")
	}
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID {
			continue
		}
		if pattern.Kind == "drums" || index >= len(pattern.Steps) {
			return nil, fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
		}
		token := pattern.Steps[index]
		if token.Text == "." || token.Text == "-" {
			return nil, fmt.Errorf("step %d needs a note before setting %s", index+1, modifier)
		}
		start, err := Offset(source, ctx.Options.Path, token.Position)
		if err != nil {
			return nil, err
		}
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		mark, before := "^", "~*?%"
		if modifier == "slide" {
			mark, before = "~", "*?%"
		}
		replacement := token.Text
		if strings.Contains(replacement, mark) {
			replacement = strings.ReplaceAll(replacement, mark, "")
		} else if at := strings.IndexAny(replacement, before); at >= 0 {
			replacement = replacement[:at] + mark + replacement[at:]
		} else {
			replacement += mark
		}
		updated := make([]byte, 0, len(source)-len(token.Text)+len(replacement))
		updated = append(updated, source[:start]...)
		updated = append(updated, replacement...)
		updated = append(updated, source[end:]...)
		return updated, nil
	}
	return nil, fmt.Errorf("unknown pattern %q", patternID)
}

// cycledStepSource writes a musician-facing preset into the authored note.
// A phrase expansion points to the shared phrase token, as other grid edits do.
func cycledStepSource(ctx *Context, patternID, laneID string, index int, kind string) ([]byte, error) {
	source := ctx.Source
	if patternID == "" || laneID != "" || index < 0 || kind != "ratchet" && kind != "chance" {
		return nil, fmt.Errorf("a note pattern, nonnegative step, and ratchet or chance are required")
	}
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID {
			continue
		}
		if pattern.Kind == "drums" || index >= len(pattern.Steps) {
			return nil, fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
		}
		token := pattern.Steps[index]
		if token.Text == "." || token.Text == "-" {
			return nil, fmt.Errorf("step %d needs a note before changing %s", index+1, kind)
		}
		start, err := Offset(source, ctx.Options.Path, token.Position)
		if err != nil {
			return nil, err
		}
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		value := 1
		at := strings.IndexByte(token.Text, '*')
		if kind == "chance" {
			value, at = 100, strings.IndexAny(token.Text, "?%")
		}
		replacement := token.Text
		if at >= 0 {
			last := at + 1
			for last < len(replacement) && replacement[last] >= '0' && replacement[last] <= '9' {
				last++
			}
			parsed, err := strconv.Atoi(replacement[at+1 : last])
			if err != nil {
				return nil, fmt.Errorf("invalid %s on step %d: %w", kind, index+1, err)
			}
			value = parsed
			replacement = replacement[:at] + replacement[last:]
		}
		if kind == "ratchet" {
			if value < 1 || value > 8 {
				return nil, fmt.Errorf("ratchet on step %d is outside 1–8", index+1)
			}
			next := value%8 + 1
			if next > 1 {
				mark := "*" + strconv.Itoa(next)
				if before := strings.IndexAny(replacement, "?%"); before >= 0 {
					replacement = replacement[:before] + mark + replacement[before:]
				} else {
					replacement += mark
				}
			}
		} else {
			if value < 1 || value > 100 {
				return nil, fmt.Errorf("chance on step %d is outside 1–100", index+1)
			}
			switch {
			case value > 75:
				replacement += "?75"
			case value > 50:
				replacement += "?50"
			case value > 25:
				replacement += "?25"
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

func drumVelocitySource(ctx *Context, patternID, laneID string, index, velocity int) ([]byte, error) {
	source := ctx.Source
	if velocity < 1 || velocity > 127 {
		return nil, fmt.Errorf("drum velocity must be in MIDI range 1–127")
	}
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
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
			start, err := Offset(source, ctx.Options.Path, token.Position)
			if err != nil {
				return nil, err
			}
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
