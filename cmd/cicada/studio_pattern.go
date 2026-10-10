package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioPatternSettings struct {
	Swing100  int `json:"swing100"`
	Gate      int `json:"gate"`
	Transpose int `json:"transpose"`
}

type studioStepEdit struct {
	Mode     string `json:"mode"`
	Pitch    int    `json:"pitch"`
	Accent   bool   `json:"accent"`
	Slide    bool   `json:"slide"`
	Ratchet  int    `json:"ratchet"`
	Chance   int    `json:"chance"`
	Velocity string `json:"velocity"`
}

type studioPatternRange struct {
	Operation string `json:"operation"`
	First     int    `json:"first"`
	Last      int    `json:"last"`
	Target    int    `json:"target"`
	Amount    int    `json:"amount"`
}

func studioStepEntity(edit studioEdit) edits.EntityID {
	if edit.Lane != "" {
		return edits.EntityID(fmt.Sprintf("step:%s/%s/%d", edit.Pattern, edit.Lane, edit.Step))
	}
	return edits.EntityID(fmt.Sprintf("step:%s/%d", edit.Pattern, edit.Step))
}

func (s *studio) editPattern(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	entity := edits.EntityID("pattern:" + edit.Pattern)
	env := edits.Envelope{}
	var intent edits.Intent
	switch edit.Action {
	case "settings":
		in := &edits.SetPatternSettings{Entity: entity}
		if edit.Settings != nil {
			in.Swing100, in.Gate, in.Transpose = edit.Settings.Swing100, edit.Settings.Gate, edit.Settings.Transpose
		}
		intent = in
	case "step":
		in := &edits.SetStep{Entity: studioStepEntity(edit), Shared: "pattern"}
		if edit.NoteEdit != nil {
			n := edit.NoteEdit
			in.Mode, in.Pitch, in.Accent, in.Slide, in.Ratchet, in.Chance, in.Velocity = n.Mode, n.Pitch, n.Accent, n.Slide, n.Ratchet, n.Chance, n.Velocity
		}
		intent = in
	case "duplicate":
		intent = &edits.DuplicatePattern{Entity: entity, Name: edit.NewName}
	case "range":
		in := &edits.SetRange{Entity: entity, Lane: edit.Lane, First: -1, Shared: "pattern"}
		if edit.Range != nil {
			r := edit.Range
			in.Operation, in.First, in.Last, in.Target, in.Amount = r.Operation, r.First, r.Last, r.Target, r.Amount
		}
		intent = in
	case "resize":
		intent = &edits.ResizePattern{Entity: entity, Length: edit.Length, Shared: "pattern"}
	case "toggle":
		intent = &edits.ToggleStep{Entity: studioStepEntity(edit), Shared: "pattern"}
	case "pitch":
		if edit.Pitch != nil {
			intent = &edits.SetPitch{Entity: studioStepEntity(edit), Pitch: *edit.Pitch, Shared: "pattern"}
		} else {
			// Use the public decoder's required-pitch semantics. Expansion and
			// validation still run before the old "choose a pitch" refusal.
			raw, _ := json.Marshal(map[string]any{"version": 1, "intents": []any{map[string]any{"kind": "setpitch", "entity": studioStepEntity(edit), "shared": "pattern"}}})
			if err := json.Unmarshal(raw, &env); err != nil {
				studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			intent = env.Intents[0]
		}
	case "bind":
		intent = &edits.BindScene{Scene: edit.Scene, Track: edit.Track, Pattern: edit.Pattern}
	default:
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown pattern action"})
		return
	}
	env.Intents = []edits.Intent{intent}
	edit.Label = studioEditLabel(edit)
	s.applyIntents(w, edit, env, edits.ParamWriterAuto, nil)
}

type studioSpanEdit struct {
	start, end int
	text       string
}

// Apply disjoint CST patches from the end so offsets refer to the original
// source throughout. Everything outside these spans remains byte-identical.
func patchStudioSpans(source []byte, edits []studioSpanEdit) ([]byte, error) {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	updated := bytes.Clone(source)
	limit := len(source)
	for _, edit := range edits {
		if edit.start < 0 || edit.end < edit.start || edit.end > limit {
			return nil, fmt.Errorf("overlapping or invalid source patches")
		}
		var err error
		updated, err = replaceSongSpan(updated, edit.start, edit.end, []byte(edit.text))
		if err != nil {
			return nil, err
		}
		limit = edit.start
	}
	return updated, nil
}

func studioDeclaration(source []byte, types []string, name string) (*gts.Node, *walk.Walker, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, nil, err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		for _, kind := range types {
			if walker.Type(node) == kind && walker.Text(walker.Field(node, "name")) == name {
				return node, walker, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("unknown declaration %q", name)
}

var studioPatternTypes = []string{"acid_pattern", "note_pattern", "drum_pattern"}

func patternSettingsSource(source []byte, id string, settings *studioPatternSettings) ([]byte, error) {
	if settings == nil || settings.Swing100 < 5000 || settings.Swing100 > 7500 || settings.Gate < 10 || settings.Gate > 100 || settings.Transpose < -24 || settings.Transpose > 24 {
		return nil, fmt.Errorf("swing must be 50–75%%, gate 10–100%%, and transpose −24 to +24 semitones")
	}
	node, walker, err := studioDeclaration(source, studioPatternTypes, id)
	if err != nil {
		return nil, err
	}
	if walker.Type(node) == "drum_pattern" && settings.Transpose != 0 {
		return nil, fmt.Errorf("drum lanes cannot transpose")
	}
	values := map[string]string{"swing": strconv.FormatFloat(float64(settings.Swing100)/100, 'f', -1, 64) + "%", "gate": strconv.Itoa(settings.Gate) + "%", "transpose": strconv.Itoa(settings.Transpose)}
	var edits []studioSpanEdit
	for i := 0; i < node.NamedChildCount(); i++ {
		attr := node.NamedChild(i)
		if walker.Type(attr) != "pattern_attr" {
			continue
		}
		name := walker.Text(walker.Field(attr, "name"))
		if text, ok := values[name]; ok {
			value := walker.Field(attr, "value")
			edits = append(edits, studioSpanEdit{int(value.StartByte()), int(value.EndByte()), text})
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
		edits = append(edits, studioSpanEdit{at, at, missing + " "})
	}
	return patchStudioSpans(source, edits)
}

// Phrase-based patterns become locally editable without changing the shared
// phrase or another pattern. Only each `use` span is expanded; its surrounding
// comments and direct note spellings stay intact.
func independentPatternSource(source []byte, id string) ([]byte, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before editing a pattern")
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must compile before editing a pattern")
	}
	node, walker, err := studioDeclaration(source, studioPatternTypes, id)
	if err != nil {
		return nil, err
	}
	var pattern *project.Pattern
	for i := range semantic.Patterns {
		if semantic.Patterns[i].ID == id {
			pattern = &semantic.Patterns[i]
			break
		}
	}
	if pattern == nil {
		return nil, fmt.Errorf("unknown pattern %q", id)
	}
	phrases := map[string]notation.Phrase{}
	for _, phrase := range score.Phrases {
		phrases[phrase.Name] = phrase
	}
	var edits []studioSpanEdit
	index := 0
	for i := 0; i < node.NamedChildCount(); i++ {
		part := node.NamedChild(i)
		switch walker.Type(part) {
		case "acid_step":
			if walker.Text(part) != "|" {
				index++
			}
		case "phrase_use":
			if strings.Contains(walker.Text(part), "//") {
				return nil, fmt.Errorf("comments inside a phrase use need a source edit to preserve their attachment")
			}
			phrase, ok := phrases[walker.Text(walker.Field(part, "name"))]
			if !ok {
				return nil, fmt.Errorf("unknown phrase")
			}
			repeat := 1
			if count := walker.Field(part, "repeat"); count != nil {
				repeat, err = strconv.Atoi(walker.Text(count))
				if err != nil || repeat < 1 || repeat > 64 {
					return nil, fmt.Errorf("invalid phrase repeat")
				}
			}
			transpose := walker.Field(part, "transpose")
			transformed := transpose != nil && walker.Text(transpose) != "0"
			var tokens []string
			for range repeat {
				for _, token := range phrase.Steps {
					if index >= len(pattern.Data) {
						return nil, fmt.Errorf("phrase expansion no longer matches the pattern")
					}
					text := token.Text
					if step := pattern.Data[index]; transformed && step != nil && !step.Tie {
						text = sourcePitch(int(step.Note)) + noteSuffix(token.Text)
					}
					tokens = append(tokens, text)
					index++
				}
			}
			edits = append(edits, studioSpanEdit{int(part.StartByte()), int(part.EndByte()), strings.Join(tokens, " ")})
		}
	}
	return patchStudioSpans(source, edits)
}

func noteSuffix(text string) string {
	if at := strings.IndexAny(text, "^~*?%"); at >= 0 {
		return text[at:]
	}
	return ""
}

func patternStepSource(source []byte, id, lane string, index int, edit *studioStepEdit) ([]byte, error) {
	if edit == nil || index < 0 || edit.Ratchet < 1 || edit.Ratchet > 8 || edit.Chance < 1 || edit.Chance > 100 {
		return nil, fmt.Errorf("choose a step, ratchet 1–8, and chance 1–100%%")
	}
	updated, err := independentPatternSource(source, id)
	if err != nil {
		return nil, err
	}
	score, diagnostics := notation.Parse(updated)
	if score == nil || hasDiagnosticErrors(diagnostics) {
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
			text = sourcePitch(edit.Pitch)
			// Saving dynamics on the same pitch preserves its authored spelling.
			current, err := studioPatternStep(updated, id, index)
			if err != nil {
				return nil, err
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
			if edit.Velocity == "x" || edit.Velocity == "X" {
				text = edit.Velocity
			} else if len(edit.Velocity) == 1 && edit.Velocity[0] >= '1' && edit.Velocity[0] <= '9' {
				text = "x" + edit.Velocity
			} else {
				return nil, fmt.Errorf("choose a drum velocity from x1–x9, normal x, or accented X")
			}
		default:
			return nil, fmt.Errorf("choose note, hit, tie, or rest")
		}
		if edit.Mode == "note" || edit.Mode == "hit" {
			if edit.Ratchet > 1 {
				text += "*" + strconv.Itoa(edit.Ratchet)
			}
			if edit.Chance < 100 {
				text += "?" + strconv.Itoa(edit.Chance)
			}
		}
		start := studioSourceOffset(updated, token.Position)
		if start < 0 || start+len(token.Text) > len(updated) || string(updated[start:start+len(token.Text)]) != token.Text {
			return nil, fmt.Errorf("step no longer matches its source")
		}
		return replaceSongSpan(updated, start, start+len(token.Text), []byte(text))
	}
	return nil, fmt.Errorf("unknown pattern %q", id)
}

var studioPatternName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,63}$`)

func duplicatePatternSource(source []byte, id, name string) ([]byte, error) {
	if !studioPatternName.MatchString(name) {
		return nil, fmt.Errorf("name must start with a lowercase letter or underscore and contain at most 64 letters, digits, underscores, or hyphens")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before duplicating a pattern")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name == name {
			return nil, fmt.Errorf("pattern %q already exists", name)
		}
	}
	independent, err := independentPatternSource(source, id)
	if err != nil {
		return nil, err
	}
	// Unassigned patterns use the default home octave. Fix the clone's notes
	// to their current MIDI pitches so a custom voice's octave does not change
	// its music while it waits to be assigned to a track.
	score, ds := notation.Parse(independent)
	semantic, ds2 := project.FromScore(score)
	if hasDiagnosticErrors(ds) || semantic == nil || hasDiagnosticErrors(ds2) {
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
			var notes []studioSpanEdit
			for i, token := range authored.Steps {
				if note := pattern.Data[i]; note != nil && !note.Tie {
					start := studioSourceOffset(independent, token.Position)
					notes = append(notes, studioSpanEdit{start, start + len(token.Text), sourcePitch(int(note.Note)) + noteSuffix(token.Text)})
				}
			}
			independent, err = patchStudioSpans(independent, notes)
			if err != nil {
				return nil, err
			}
		}
	}
	node, walker, err := studioDeclaration(independent, studioPatternTypes, id)
	if err != nil {
		return nil, err
	}
	start, end := int(node.StartByte()), int(node.EndByte())
	identifier := walker.Field(node, "name")
	edits := []studioSpanEdit{{int(identifier.StartByte()) - start, int(identifier.EndByte()) - start, name}}
	// An explicit slot is placement metadata. A new variation gets a free
	// slot when assigned to a scene instead of colliding with its parent.
	for i := 0; i < node.NamedChildCount(); i++ {
		attr := node.NamedChild(i)
		if walker.Type(attr) == "pattern_attr" && walker.Text(walker.Field(attr, "name")) == "slot" {
			edits = append(edits, studioSpanEdit{int(attr.StartByte()) - start, int(attr.EndByte()) - start, ""})
		}
	}
	clone, err := patchStudioSpans(independent[start:end], edits)
	if err != nil {
		return nil, err
	}
	original, _, err := studioDeclaration(source, studioPatternTypes, id)
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
	return replaceSongSpan(source, at, at, append([]byte(newline+newline), clone...))
}

// Range edits share the same atomic revision and Undo entry as a single
// step. Source gaps (including comments and bar separators) remain intact.
func patternRangeSource(source []byte, id, lane string, edit *studioPatternRange) ([]byte, error) {
	if edit == nil || edit.First < 0 || edit.Last < edit.First || edit.Amount < -127 || edit.Amount > 127 {
		return nil, fmt.Errorf("choose a valid step range and shift −127 to +127")
	}
	local, err := independentPatternSource(source, id)
	if err != nil {
		return nil, err
	}
	score, _ := notation.Parse(local)
	semantic, ds := project.FromScore(score)
	if semantic == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("pattern must compile")
	}
	for _, authored := range score.Patterns {
		if authored.Name != id {
			continue
		}
		var pattern project.Pattern
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
					pitch := int(note.Note) + edit.Amount
					if pitch < 0 || pitch > 127 {
						return nil, fmt.Errorf("transposed note would leave MIDI range 0–127")
					}
					texts[i] = sourcePitch(pitch) + noteSuffix(texts[i])
				}
			}
		default:
			return nil, fmt.Errorf("choose clear, copy, reverse, rotate, or transpose")
		}
		var edits []studioSpanEdit
		for i, text := range texts {
			token := tokens[start+i]
			at := studioSourceOffset(local, token.Position)
			edits = append(edits, studioSpanEdit{at, at + len(token.Text), text})
		}
		return patchStudioSpans(local, edits)
	}
	return nil, fmt.Errorf("unknown pattern %q", id)
}

func resizePatternSource(source []byte, id string, length int) ([]byte, error) {
	if length < 1 || length > 64 {
		return nil, fmt.Errorf("patterns must have 1–64 steps")
	}
	if score, diagnostics := notation.Parse(source); score != nil && !hasDiagnosticErrors(diagnostics) {
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
	local, err := independentPatternSource(source, id)
	if err != nil {
		return nil, err
	}
	score, _ := notation.Parse(local)
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
		var edits []studioSpanEdit
		for _, tokens := range lanes {
			if len(tokens) < 1 {
				return nil, fmt.Errorf("empty pattern lane")
			}
			if length > len(tokens) {
				last := tokens[len(tokens)-1]
				at := studioSourceOffset(local, last.Position) + len(last.Text)
				text := strings.Repeat(" .", length-len(tokens))
				edits = append(edits, studioSpanEdit{at, at, text})
			} else {
				for _, token := range tokens[length:] {
					at := studioSourceOffset(local, token.Position)
					edits = append(edits, studioSpanEdit{at, at + len(token.Text), ""})
				}
			}
		}
		for _, attr := range pattern.Attrs {
			if attr.Name == "steps" {
				at := studioSourceOffset(local, attr.ValuePosition)
				edits = append(edits, studioSpanEdit{at, at + len(attr.Value), strconv.Itoa(length)})
			}
		}
		return patchStudioSpans(local, edits)
	}
	return nil, fmt.Errorf("unknown pattern %q", id)
}

func bindPatternSource(source []byte, sceneID, trackID, patternID string) ([]byte, error) {
	node, walker, err := studioDeclaration(source, []string{"scene_decl"}, sceneID)
	if err != nil {
		return nil, err
	}
	if _, _, err := studioDeclaration(source, []string{"track_decl"}, trackID); err != nil {
		return nil, err
	}
	if patternID != "off" && patternID != "keep" {
		if _, _, err := studioDeclaration(source, append(append([]string{}, studioPatternTypes...), "clip_decl"), patternID); err != nil {
			return nil, err
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		binding := node.NamedChild(i)
		if walker.Type(binding) == "scene_assignment" && walker.Text(walker.Field(binding, "target")) == trackID {
			value := walker.Field(binding, "value")
			return replaceSongSpan(source, int(value.StartByte()), int(value.EndByte()), []byte(patternID))
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(node.EndByte()) - 1
	return replaceSongSpan(source, at, at, []byte(newline+"  "+trackID+" = "+patternID+newline))
}
