package edit

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

// SetPatternSettings changes a pattern's swing, gate and transpose without
// changing its note text. A drum pattern keeps transpose 0.
//
// Example: {"kind":"setpatternsettings","entity":"pattern:riff","swing100":5600,"gate":60,"transpose":0}
type SetPatternSettings struct {
	Entity    EntityID `json:"entity" cicada:"required"`                       // the pattern, as pattern:<name>
	Swing100  int      `json:"swing100" unit:"0.01 percent" cicada:"required"` // swing in hundredths of a percent, 5000–7500 (50% to 75%)
	Gate      int      `json:"gate" unit:"percent" cicada:"required"`          // gate length, 10–100
	Transpose int      `json:"transpose" unit:"semitones"`                     // −24 to +24
}

func (SetPatternSettings) Kind() string { return "setpatternsettings" }

// SetStep writes one step: a note, a drum hit, a tie or a rest. A note on a
// chord step moves the whole chord. Shared uses ToggleStep's JSON schema.
//
// Example: {"kind":"setstep","entity":"step:riff/1","mode":"note","pitch":45,"accent":true,"slide":false,"ratchet":1,"chance":100}
// Example: {"kind":"setstep","entity":"step:beat/bd/1","mode":"hit","velocity":"X","ratchet":1,"chance":100}
// Example: {"kind":"setstep","entity":"step:riff/1","mode":"tie","ratchet":1,"chance":100}
// Example: {"kind":"setstep","entity":"step:riff/0","mode":"rest","ratchet":1,"chance":100}
type SetStep struct {
	Entity   EntityID `json:"entity" cicada:"required"`                                  // the step: step:<pattern>/<index>, or step:<pattern>/<lane>/<index> on a drum pattern
	Mode     string   `json:"mode" values:"step-mode" cicada:"required,selects"`         // hit needs a drum pattern; tie and note need a note pattern
	Pitch    int      `json:"pitch"`                                                     // MIDI pitch 0–127 for mode note
	Accent   bool     `json:"accent"`                                                    // accent a note
	Slide    bool     `json:"slide"`                                                     // slide from a note into the next one
	Ratchet  int      `json:"ratchet" cicada:"required"`                                 // repeats within the step; 1 plays once
	Chance   int      `json:"chance" unit:"percent" cicada:"required"`                   // probability the step plays, 1–100
	Velocity string   `json:"velocity,omitempty" values:"drum-velocity" when:"mode=hit"` // drum hit spelling for mode hit: x, accented X, or a level 1–9
	Shared   string   `json:"shared,omitempty" values:"shared"`                          // as for togglestep
}

func (SetStep) Kind() string { return "setstep" }

// stepModes, drumVelocities and rangeOperations are what SetStep's mode and
// velocity and SetRange's operation accept.
var (
	stepModes       = valueList("step-mode", "note", "hit", "tie", "rest")
	drumVelocities  = valueList("drum-velocity", "x", "X", "1", "2", "3", "4", "5", "6", "7", "8", "9")
	rangeOperations = valueList("range-operation", "clear", "copy", "reverse", "rotate", "transpose")
)

// DuplicatePattern copies a pattern under a new name. With a track, the copy
// is written for that track's instrument, and the edit is refused unless the
// copy renders the same on it.
//
// Example: {"kind":"duplicatepattern","entity":"pattern:riff","name":"riff-b"}
type DuplicatePattern struct {
	Entity EntityID `json:"entity" cicada:"required"` // the pattern to copy, as pattern:<name>
	Name   string   `json:"name" cicada:"required"`   // the new pattern's name: lowercase letters, digits, _ and -, at most 64 bytes
	Track  string   `json:"track,omitempty"`          // the track the copy is for
}

func (DuplicatePattern) Kind() string { return "duplicatepattern" }

// SetRange clears, copies, reverses, rotates or transposes a range of steps
// in one pattern lane. It edits the selected source and, for copy, its
// destination. Shared uses ToggleStep's JSON schema; detach expands only uses
// touched by the edit.
//
// Example: {"kind":"setrange","entity":"pattern:riff","operation":"transpose","first":0,"last":3,"amount":12}
// Example: {"kind":"setrange","entity":"pattern:riff","operation":"clear","first":0,"last":1}
// Example: {"kind":"setrange","entity":"pattern:riff","operation":"copy","first":0,"last":1,"target":4}
// Example: {"kind":"setrange","entity":"pattern:riff","operation":"reverse","first":0,"last":3}
// Example: {"kind":"setrange","entity":"pattern:riff","operation":"rotate","first":0,"last":3,"amount":1}
type SetRange struct {
	Entity    EntityID `json:"entity" cicada:"required"`                                     // the pattern, as pattern:<name>
	Lane      string   `json:"lane,omitempty"`                                               // the drum lane; empty for a note pattern
	Operation string   `json:"operation" values:"range-operation" cicada:"required,selects"` // what to do with the steps
	First     int      `json:"first"`                                                        // first step index, from 0
	Last      int      `json:"last"`                                                         // last step index, inclusive
	Target    int      `json:"target,omitempty"`                                             // first step index of the copy, for copy
	Amount    int      `json:"amount,omitempty"`                                             // steps to rotate, or semitones to transpose, −127 to +127
	Shared    string   `json:"shared,omitempty" values:"shared"`                             // as for togglestep
}

func (SetRange) Kind() string { return "setrange" }

// ResizePattern changes a pattern's length in steps, adding rests at the end
// or removing the last steps. Shared uses ToggleStep's JSON schema; resizing
// touches every use, so detach expands them all.
//
// Example: {"kind":"resizepattern","entity":"pattern:riff","length":16}
type ResizePattern struct {
	Entity EntityID `json:"entity" cicada:"required"`         // the pattern, as pattern:<name>
	Length int      `json:"length" cicada:"required"`         // the new number of steps
	Shared string   `json:"shared,omitempty" values:"shared"` // as for togglestep
}

func (ResizePattern) Kind() string { return "resizepattern" }

// BindScene makes a scene play a pattern on a track, replacing what the
// scene played there before.
//
// Example: {"kind":"bindscene","scene":"break","track":"drums","pattern":"beat"}
type BindScene struct {
	Scene   string `json:"scene" cicada:"required"`   // the scene's name
	Track   string `json:"track" cicada:"required"`   // the track's name
	Pattern string `json:"pattern" cicada:"required"` // the pattern's name
}

func (BindScene) Kind() string { return "bindscene" }

func init() {
	Register("setpatternsettings", func() Intent { return &SetPatternSettings{} })
	Register("setstep", func() Intent { return &SetStep{} })
	Register("duplicatepattern", func() Intent { return &DuplicatePattern{} })
	Register("setrange", func() Intent { return &SetRange{} })
	Register("resizepattern", func() Intent { return &ResizePattern{} })
	Register("bindscene", func() Intent { return &BindScene{} })
	for _, kind := range []string{"setpatternsettings", "setstep", "duplicatepattern", "setrange", "resizepattern", "bindscene"} {
		Handle(kind, gridSourceEdit(editPattern))
	}
}

func patternTarget(entity EntityID) (string, error) {
	if _, err := ParseEntityID(string(entity)); err != nil {
		return "", entityError(err)
	}
	if entity.Kind() != KindPattern {
		return "", entityError(fmt.Errorf("pattern edit needs a pattern entity, got %q", entity))
	}
	return entity.Name(), nil
}

func editPattern(ctx *Context, intent Intent) error {
	if err := patternPreflight(ctx); err != nil {
		return err
	}
	var updated []byte
	var label string
	var err error
	switch in := intent.(type) {
	case *SetPatternSettings:
		id, e := patternTarget(in.Entity)
		if e != nil {
			return e
		}
		updated, err = patternSettingsSource(ctx, id, in)
		label = fmt.Sprintf("Pattern · %s timing and transpose changed", id)
	case *SetStep:
		id, lane, index, e := stepTarget(in.Entity)
		if e != nil {
			return e
		}
		updated, err = patternStepSource(ctx, id, lane, index, in)
		label = fmt.Sprintf("Pattern · %s / %s step %d edited", id, lane, index+1)
	case *DuplicatePattern:
		id, e := patternTarget(in.Entity)
		if e != nil {
			return e
		}
		updated, err = duplicatePatternSource(ctx, id, in.Name, in.Track)
		label = fmt.Sprintf("Pattern · %s duplicated as %s", id, in.Name)
	case *SetRange:
		id, e := patternTarget(in.Entity)
		if e != nil {
			return e
		}
		updated, err = patternRangeSource(ctx, id, in.Lane, in)
		label = fmt.Sprintf("Pattern · %s / %s steps %d–%d · %s", id, in.Lane, in.First+1, in.Last+1, in.Operation)
	case *ResizePattern:
		id, e := patternTarget(in.Entity)
		if e != nil {
			return e
		}
		updated, err = resizePatternSource(ctx, id, in.Length, in.Shared)
		label = fmt.Sprintf("Pattern · %s resized to %d steps", id, in.Length)
	case *BindScene:
		updated, err = bindPatternSource(ctx, in.Scene, in.Track, in.Pattern)
		label = fmt.Sprintf("Scene · %s / %s assigned %s", in.Scene, in.Track, in.Pattern)
	}
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(label)
	return nil
}

// Hosts that supply project parsing retain the old route's validation before
// invoking a standalone writer. The inherited edition stays in Options.
func patternPreflight(ctx *Context) error {
	if ctx.Options.ParseProject == nil {
		return nil
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("score must validate before editing")
	}
	return nil
}

// Intermediate expansions use the inherited edition without a synthetic
// header and compile against their own bytes instead of a cached earlier plan.
func sourceContext(ctx *Context, source []byte) *Context {
	if bytes.Equal(ctx.Source, source) {
		return ctx
	}
	working := *ctx
	working.Source, working.plan = source, nil
	working.files = append([]File(nil), ctx.files...)
	working.fileIndex = maps.Clone(ctx.fileIndex)
	return &working
}

func resolvePatternRange(ctx *Context, id, lane string, first, last int, policy string) ([]byte, error) {
	if policy == "pattern" {
		return independentPatternSource(ctx, id)
	}
	source := ctx.Source
	for index := first; index <= last; index++ {
		entity := EntityID(fmt.Sprintf("step:%s/%d", id, index))
		if lane != "" {
			entity = EntityID(fmt.Sprintf("step:%s/%s/%d", id, lane, index))
		}
		var err error
		source, err = resolveShared(sourceContext(ctx, source), entity, policy)
		if err != nil {
			return nil, err
		}
	}
	return source, nil
}

func patternStep(ctx *Context, id string, index int) (*Step, error) {
	score, ds := ctx.Parse()
	if score == nil || hasErrors(ds) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	plan, err := ctx.CurrentPlan()
	if err != nil || plan == nil {
		return nil, fmt.Errorf("score must compile before a grid edit")
	}
	for _, p := range plan.Patterns {
		if p.ID == id {
			if p.Kind == "drums" || index < 0 || index >= len(p.Data) {
				return nil, fmt.Errorf("pattern %s has no note step %d", id, index+1)
			}
			return p.Data[index], nil
		}
	}
	return nil, entityError(fmt.Errorf("unknown pattern %q", id))
}

func patternSettingsSource(ctx *Context, id string, settings *SetPatternSettings) ([]byte, error) {
	source := ctx.Source
	if settings == nil || settings.Swing100 < 5000 || settings.Swing100 > 7500 || settings.Gate < 10 || settings.Gate > 100 || settings.Transpose < -24 || settings.Transpose > 24 {
		return nil, fmt.Errorf("swing must be 50–75%%, gate 10–100%%, and transpose −24 to +24 semitones")
	}
	node, walker, err := declaration(source, patternTypes, id)
	if err != nil {
		return nil, err
	}
	if walker.Type(node) == "drum_pattern" && settings.Transpose != 0 {
		return nil, fmt.Errorf("drum lanes cannot transpose")
	}
	values := map[string]string{"swing": strconv.FormatFloat(float64(settings.Swing100)/100, 'f', -1, 64) + "%", "gate": strconv.Itoa(settings.Gate) + "%", "transpose": strconv.Itoa(settings.Transpose)}
	var edits []Span
	for i := 0; i < node.NamedChildCount(); i++ {
		attr := node.NamedChild(i)
		if walker.Type(attr) != "pattern_attr" {
			continue
		}
		name := walker.Text(walker.Field(attr, "name"))
		if text, ok := values[name]; ok {
			value := walker.Field(attr, "value")
			edits = append(edits, Span{int(value.StartByte()), int(value.EndByte()), text, ctx.Options.Path})
			delete(values, name)
		}
	}
	missing := ""
	for _, name := range []string{"swing", "gate", "transpose"} {
		if value, ok := values[name]; ok {
			missing += " " + name + " = " + value
		}
	}
	if missing != "" {
		content := source[node.StartByte():node.EndByte()]
		at := int(node.StartByte()) + bytes.IndexByte(content, '{') + 1
		edits = append(edits, Span{at, at, missing + " ", ctx.Options.Path})
	}
	return PatchSpans(source, ctx.Options.Path, edits)
}

func patternStepSource(ctx *Context, id, lane string, index int, edit *SetStep) ([]byte, error) {
	if edit == nil || index < 0 || edit.Ratchet < 1 || edit.Ratchet > 8 || edit.Chance < 1 || edit.Chance > 100 {
		return nil, fmt.Errorf("choose a step, ratchet 1–8, and chance 1–100%%")
	}
	updated, err := resolveShared(ctx, edit.Entity, edit.Shared)
	if err != nil {
		return nil, err
	}
	score, diagnostics := sourceContext(ctx, updated).Parse()
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("expanded pattern must validate")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != id {
			continue
		}
		steps := pattern.Steps
		drums := pattern.Kind == "drums"
		if drums {
			steps = nil
			for _, row := range pattern.Lanes {
				if row.Name == lane {
					steps = row.Hits
				}
			}
		} else if lane != "" {
			return nil, fmt.Errorf("note patterns do not have drum lanes")
		}
		if index >= len(steps) {
			return nil, fmt.Errorf("step is outside the authored pattern lane")
		}
		token := steps[index]
		text := "."
		chordWritten := false
		if !containsString(stepModes, edit.Mode) {
			return nil, fmt.Errorf("choose note, hit, tie, or rest")
		}
		switch edit.Mode {
		case "rest":
		case "tie":
			if drums {
				return nil, fmt.Errorf("drum hits cannot tie")
			}
			text = "-"
		case "note":
			if drums || edit.Pitch < 0 || edit.Pitch > 127 {
				return nil, fmt.Errorf("choose a MIDI note from 0 to 127")
			}
			sourceNote := edit.Pitch - token.Transpose
			if sourceNote < 0 || sourceNote > 127 {
				return nil, fmt.Errorf("choose a MIDI note from 0 to 127")
			}
			text = sourcePitch(sourceNote)
			// Saving dynamics on the same pitch preserves its authored spelling.
			current, err := patternStep(sourceContext(ctx, updated), id, index)
			if err != nil {
				return nil, err
			}
			if current != nil && len(current.Notes) > 0 {
				if edit.Slide || edit.Ratchet != 1 {
					return nil, fmt.Errorf("chords cannot slide or ratchet")
				}
				shifted := make([]int, len(current.Notes))
				for i, pitch := range current.Notes {
					shifted[i] = pitch + edit.Pitch - int(current.Note) - token.Transpose
					if shifted[i] < 0 || shifted[i] > 127 || shifted[i]+token.Transpose < 0 || shifted[i]+token.Transpose > 127 {
						return nil, fmt.Errorf("transposed note would leave MIDI range 0–127")
					}
				}
				suffix := ""
				if edit.Accent {
					suffix += "^"
				}
				if edit.Chance < 100 {
					suffix += "?" + strconv.Itoa(edit.Chance)
				}
				if edit.Accent == current.Accent && edit.Chance == int(current.Probability) {
					suffix = token.ChordModifiers
				}
				text, err = chordSource(token, shifted, suffix, current)
				if err != nil {
					return nil, err
				}
				chordWritten = true
				break
			}
			if current != nil && !current.Tie && int(current.Note) == edit.Pitch {
				text = strings.TrimSuffix(token.Text, noteSuffix(token.Text))
			}
			if edit.Accent {
				text += "^"
			}
			if edit.Slide {
				text += "~"
			}
		case "hit":
			if !drums {
				return nil, fmt.Errorf("hits require a drum pattern")
			}
			if !containsString(drumVelocities, edit.Velocity) {
				return nil, fmt.Errorf("choose a drum velocity from x1–x9, normal x, or accented X")
			}
			text = edit.Velocity
			if edit.Velocity != "x" && edit.Velocity != "X" {
				text = "x" + edit.Velocity
			}
		default:
			return nil, fmt.Errorf("choose note, hit, tie, or rest")
		}
		if !chordWritten && (edit.Mode == "note" || edit.Mode == "hit") {
			if edit.Ratchet > 1 {
				text += "*" + strconv.Itoa(edit.Ratchet)
			}
			if edit.Chance < 100 {
				text += "?" + strconv.Itoa(edit.Chance)
			}
		}
		start, err := Offset(updated, ctx.Options.Path, token.Position)
		if err != nil {
			return nil, err
		}
		if start < 0 || start+len(token.Text) > len(updated) || string(updated[start:start+len(token.Text)]) != token.Text {
			return nil, fmt.Errorf("step no longer matches its source")
		}
		return ReplaceSpan(updated, ctx.Options.Path, Span{start, start + len(token.Text), text, ctx.Options.Path})
	}
	return nil, entityError(fmt.Errorf("unknown pattern %q", id))
}

var patternName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,63}$`)

func duplicatePatternSource(ctx *Context, id, name, track string) ([]byte, error) {
	source := ctx.Source
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return nil, err
	}
	if err := patternCopyName(plan, name); err != nil {
		return nil, err
	}
	score, diagnostics := ctx.Parse()
	local := false
	if score != nil && !hasErrors(diagnostics) {
		for _, pattern := range score.Patterns {
			local = local || pattern.Name == id
		}
	}
	if !local || track != "" {
		clone, err := localPatternCopy(ctx, id, name, track)
		if err != nil {
			return nil, err
		}
		updated := appendPatternCopy(source, clone)
		check, err := renderCheckFor(ctx)
		if err != nil {
			return nil, err
		}
		same, err := ctx.compareRenders("DuplicatePattern", check, source, updated)
		if err != nil {
			return nil, err
		}
		if !same {
			return nil, &RefusalError{Intent: "DuplicatePattern", Cause: "creating this variation changes the rendered song"}
		}
		return updated, nil
	}
	independent, err := independentPatternSource(ctx, id)
	if err != nil {
		return nil, err
	}
	// Unassigned patterns use the default home octave. Fix the clone's notes
	// to their current MIDI pitches so a custom voice's octave does not change
	// its music while it waits to be assigned to a track.
	score, ds := sourceContext(ctx, independent).Parse()
	semantic, compileErr := sourceContext(ctx, independent).CurrentPlan()
	if hasErrors(ds) || semantic == nil || compileErr != nil {
		return nil, fmt.Errorf("variation must compile")
	}
	for _, authored := range score.Patterns {
		if authored.Name != id || authored.Kind == "drums" {
			continue
		}
		for _, pattern := range semantic.Patterns {
			if pattern.ID != id {
				continue
			}
			var notes []Span
			for i, token := range authored.Steps {
				if note := pattern.Data[i]; note != nil && !note.Tie {
					start, err := Offset(independent, ctx.Options.Path, token.Position)
					if err != nil {
						return nil, err
					}
					text, err := shiftedStepSource(token, note, 0)
					if err != nil {
						return nil, err
					}
					notes = append(notes, Span{start, start + len(token.Text), text, ctx.Options.Path})
				}
			}
			independent, err = PatchSpans(independent, ctx.Options.Path, notes)
			if err != nil {
				return nil, err
			}
		}
	}
	node, walker, err := declaration(independent, patternTypes, id)
	if err != nil {
		return nil, err
	}
	start, end := int(node.StartByte()), int(node.EndByte())
	identifier := walker.Field(node, "name")
	edits := []Span{{int(identifier.StartByte()) - start, int(identifier.EndByte()) - start, name, ctx.Options.Path}}
	// An explicit slot is placement metadata. A new variation gets a free
	// slot when assigned to a scene instead of colliding with its parent.
	for i := 0; i < node.NamedChildCount(); i++ {
		attr := node.NamedChild(i)
		if walker.Type(attr) == "pattern_attr" && walker.Text(walker.Field(attr, "name")) == "slot" {
			edits = append(edits, Span{int(attr.StartByte()) - start, int(attr.EndByte()) - start, "", ctx.Options.Path})
		}
	}
	clone, err := PatchSpans(independent[start:end], ctx.Options.Path, edits)
	if err != nil {
		return nil, err
	}
	original, _, err := declaration(source, patternTypes, id)
	if err != nil {
		return nil, err
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(original.EndByte())
	// Keep a same-line comment attached to its original declaration.
	if strings.HasPrefix(strings.TrimSpace(string(source[at:])), "//") {
		if next := bytes.IndexByte(source[at:], '\n'); next >= 0 {
			at += next + 1
		} else {
			at = len(source)
		}
	}
	return ReplaceSpan(source, ctx.Options.Path, Span{at, at, string(append([]byte(newline+newline), clone...)), ctx.Options.Path})
}

// Range edits share the same atomic revision and Undo entry as a single
// step. Source gaps (including comments and bar separators) remain intact.
func patternRangeSource(ctx *Context, id, lane string, edit *SetRange) ([]byte, error) {
	if edit == nil || edit.First < 0 || edit.Last < edit.First || edit.Amount < -127 || edit.Amount > 127 {
		return nil, fmt.Errorf("choose a valid step range and shift −127 to +127")
	}
	if !containsString(rangeOperations, edit.Operation) {
		return nil, fmt.Errorf("choose clear, copy, reverse, rotate, or transpose")
	}
	local, err := resolvePatternRange(ctx, id, lane, edit.First, edit.Last, edit.Shared)
	if err != nil {
		return nil, err
	}
	if edit.Operation == "copy" {
		local, err = resolvePatternRange(sourceContext(ctx, local), id, lane, edit.Target, edit.Target+edit.Last-edit.First, edit.Shared)
		if err != nil {
			return nil, err
		}
	}
	score, _ := sourceContext(ctx, local).Parse()
	semantic, compileErr := sourceContext(ctx, local).CurrentPlan()
	if semantic == nil || compileErr != nil {
		return nil, fmt.Errorf("pattern must compile")
	}
	for _, authored := range score.Patterns {
		if authored.Name != id {
			continue
		}
		var pattern Pattern
		for _, p := range semantic.Patterns {
			if p.ID == id {
				pattern = p
			}
		}
		tokens, notes := authored.Steps, pattern.Data
		if authored.Kind == "drums" {
			tokens, notes = nil, pattern.Lanes[lane]
			for _, row := range authored.Lanes {
				if row.Name == lane {
					tokens = row.Hits
				}
			}
		} else if lane != "" {
			return nil, fmt.Errorf("note patterns do not have drum lanes")
		}
		if edit.Last >= len(tokens) {
			return nil, fmt.Errorf("range is outside the pattern lane")
		}
		count, start := edit.Last-edit.First+1, edit.First
		texts := make([]string, count)
		for i := range texts {
			texts[i] = tokens[edit.First+i].Text
		}
		switch edit.Operation {
		case "clear":
			for i := range texts {
				texts[i] = "."
			}
		case "copy":
			if edit.Target < 0 || edit.Target+count > len(tokens) {
				return nil, fmt.Errorf("copied range would extend past the last step")
			}
			start = edit.Target
		case "reverse":
			for i, j := 0, count-1; i < j; i, j = i+1, j-1 {
				texts[i], texts[j] = texts[j], texts[i]
			}
		case "rotate":
			prior := append([]string(nil), texts...)
			shift := (edit.Amount%count + count) % count
			for i := range texts {
				texts[(i+shift)%count] = prior[i]
			}
		case "transpose":
			if authored.Kind == "drums" {
				return nil, fmt.Errorf("drum lanes cannot transpose")
			}
			for i := range texts {
				if note := notes[edit.First+i]; note != nil && !note.Tie {
					var err error
					texts[i], err = shiftedStepSource(tokens[edit.First+i], note, edit.Amount)
					if err != nil {
						return nil, err
					}
				}
			}
		default:
			return nil, fmt.Errorf("choose clear, copy, reverse, rotate, or transpose")
		}
		var edits []Span
		for i, text := range texts {
			token := tokens[start+i]
			at, err := Offset(local, ctx.Options.Path, token.Position)
			if err != nil {
				return nil, err
			}
			edits = append(edits, Span{at, at + len(token.Text), text, ctx.Options.Path})
		}
		return PatchSpans(local, ctx.Options.Path, edits)
	}
	return nil, entityError(fmt.Errorf("unknown pattern %q", id))
}

func resizePatternSource(ctx *Context, id string, length int, shared string) ([]byte, error) {
	source := ctx.Source
	if length < 1 || length > 64 {
		return nil, fmt.Errorf("patterns must have 1–64 steps")
	}
	if score, diagnostics := ctx.Parse(); score != nil && !hasErrors(diagnostics) {
		for _, pattern := range score.Patterns {
			if pattern.Name != id {
				continue
			}
			count := len(pattern.Steps)
			if len(pattern.Lanes) > 0 {
				count = len(pattern.Lanes[0].Hits)
			}
			if count == length {
				return bytes.Clone(source), nil
			}
		}
	}
	local, err := resolvePatternRange(ctx, id, "", 0, 63, shared)
	if err != nil {
		return nil, err
	}
	score, _ := sourceContext(ctx, local).Parse()
	for _, pattern := range score.Patterns {
		if pattern.Name != id {
			continue
		}
		lanes := [][]notation.StepToken{pattern.Steps}
		if pattern.Kind == "drums" {
			lanes = nil
			for _, row := range pattern.Lanes {
				lanes = append(lanes, row.Hits)
			}
		}
		var edits []Span
		for _, tokens := range lanes {
			if len(tokens) < 1 {
				return nil, fmt.Errorf("empty pattern lane")
			}
			if length > len(tokens) {
				last := tokens[len(tokens)-1]
				at, err := Offset(local, ctx.Options.Path, last.Position)
				if err != nil {
					return nil, err
				}
				at += len(last.Text)
				text := strings.Repeat(" .", length-len(tokens))
				edits = append(edits, Span{at, at, text, ctx.Options.Path})
			} else {
				for _, token := range tokens[length:] {
					at, err := Offset(local, ctx.Options.Path, token.Position)
					if err != nil {
						return nil, err
					}
					edits = append(edits, Span{at, at + len(token.Text), "", ctx.Options.Path})
				}
			}
		}
		for _, attr := range pattern.Attrs {
			if attr.Name == "steps" {
				at, err := Offset(local, ctx.Options.Path, attr.ValuePosition)
				if err != nil {
					return nil, err
				}
				edits = append(edits, Span{at, at + len(attr.Value), strconv.Itoa(length), ctx.Options.Path})
			}
		}
		return PatchSpans(local, ctx.Options.Path, edits)
	}
	return nil, entityError(fmt.Errorf("unknown pattern %q", id))
}

func bindPatternSource(ctx *Context, sceneID, trackID, patternID string) ([]byte, error) {
	score, source, path, err := sceneSource(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, track := range score.Tracks {
		found = found || track.Name == trackID
	}
	if !found {
		return nil, fmt.Errorf("unknown declaration %q", trackID)
	}
	if patternID != "off" && patternID != "keep" {
		found = false
		for _, pattern := range score.Patterns {
			found = found || pattern.Name == patternID
		}
		for _, clip := range score.Clips {
			found = found || clip.Name == patternID
		}
		if !found {
			return nil, fmt.Errorf("unknown declaration %q", patternID)
		}
	}
	updated, err := sceneBindingSource(source, path, sceneID, trackID, patternID)
	if err != nil {
		return nil, err
	}
	return stageProjectSource(ctx, path, source, updated), nil
}

func sceneBindingSource(source []byte, path, sceneID, trackID, patternID string) ([]byte, error) {
	node, walker, err := declaration(source, []string{"scene_decl"}, sceneID)
	if err != nil {
		return nil, err
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		binding := node.NamedChild(i)
		if walker.Type(binding) == "scene_assignment" && walker.Text(walker.Field(binding, "target")) == trackID && !notation.SceneAssignmentIsSetting(trackID, walker.Text(walker.Field(binding, "value"))) {
			value := walker.Field(binding, "value")
			return ReplaceSpan(source, path, Span{int(value.StartByte()), int(value.EndByte()), patternID, path})
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(node.EndByte()) - 1
	return ReplaceSpan(source, path, Span{at, at, newline + "  " + trackID + " = " + patternID + newline, path})
}
