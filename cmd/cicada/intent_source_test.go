package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

// intentSource runs one intent on source through the edit service with the
// Studio compiler, the same way a Studio route does, and returns the new
// source. Tests use it to pin exact bytes and error text.
func intentSource(t *testing.T, source []byte, writer edits.ParamWriter, intent edits.Intent) ([]byte, error) {
	t.Helper()
	path := studioTestPath(t, string(source))
	result, err := edits.Apply(source, edits.Envelope{Version: 1, Intents: []edits.Intent{intent}}, edits.Options{Compiler: &studioCompiler{path: path}, ParamWriter: writer})
	if err != nil {
		return nil, err
	}
	return result.Source, nil
}

// looseCompiler validates with the notation parser only, as the retired writers
// did: imports, assets and project limits are not checked, so fixtures can stay
// small. It supplies a plan when the score also compiles.
type looseCompiler struct{}

func (looseCompiler) Compile(source []byte, _ map[string][]byte) (*edits.Plan, error) {
	score, ds := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(ds) {
		var lines []string
		for _, d := range ds {
			if d.Severity == "error" {
				lines = append(lines, d.Message)
			}
		}
		return nil, errors.New(strings.Join(lines, "; "))
	}
	if p, ds := project.FromScore(score); p != nil && !hasDiagnosticErrors(ds) {
		return project.EditPlan(p, source), nil
	}
	return &edits.Plan{Revision: edits.Revision(source)}, nil
}

func looseIntentSource(t *testing.T, source []byte, writer edits.ParamWriter, intent edits.Intent) ([]byte, error) {
	t.Helper()
	result, err := edits.Apply(source, edits.Envelope{Version: 1, Intents: []edits.Intent{intent}}, edits.Options{Compiler: looseCompiler{}, ParamWriter: writer})
	if err != nil {
		return nil, err
	}
	return result.Source, nil
}

func intentStepEntity(pattern, lane string, index int) edits.EntityID {
	return studioStepEntity(studioEdit{Pattern: pattern, Lane: lane, Step: index})
}

func intentToggledSource(t *testing.T, source []byte, pattern, lane string, index int) ([]byte, error) {
	return intentSource(t, source, edits.ParamWriterAuto, &edits.ToggleStep{Entity: intentStepEntity(pattern, lane, index), Shared: "definition"})
}

func intentPitchedSource(t *testing.T, source []byte, pattern, lane string, index, pitch int) ([]byte, error) {
	return intentSource(t, source, edits.ParamWriterAuto, &edits.SetPitch{Entity: intentStepEntity(pattern, lane, index), Pitch: pitch, Shared: "definition"})
}

func intentModifierSource(t *testing.T, source []byte, pattern, lane string, index int, modifier string) ([]byte, error) {
	return intentSource(t, source, edits.ParamWriterAuto, &edits.ToggleModifier{Entity: intentStepEntity(pattern, lane, index), Modifier: modifier, Shared: "definition"})
}

func intentCycledSource(t *testing.T, source []byte, pattern, lane string, index int, field string) ([]byte, error) {
	return intentSource(t, source, edits.ParamWriterAuto, &edits.CycleStep{Entity: intentStepEntity(pattern, lane, index), Field: field, Shared: "definition"})
}

func intentSongSource(t *testing.T, source []byte, action string, index, target, bars int, scene string) ([]byte, error) {
	entity := edits.EntityID(fmt.Sprintf("song:%d", index))
	var intent edits.Intent
	switch action {
	case "move":
		intent = &edits.MoveSongEntry{Entity: entity, Target: target}
	case "bars":
		intent = &edits.SetSongBars{Entity: entity, Bars: bars}
	case "append":
		intent = &edits.AppendSongEntry{Scene: scene, Bars: bars}
	case "duplicate":
		intent = &edits.DuplicateSongEntry{Entity: entity}
	case "delete":
		intent = &edits.DeleteSongEntry{Entity: entity}
	case "scene":
		intent = &edits.SetSongScene{Entity: entity, Scene: scene}
	default:
		return nil, fmt.Errorf("unknown arrangement action")
	}
	return looseIntentSource(t, source, edits.ParamWriterAuto, intent)
}

func intentSceneSetting(t *testing.T, source []byte, scene, path string, value json.RawMessage, remove bool) ([]byte, error) {
	entity := edits.EntityID("setting:" + scene + "/" + path)
	if remove {
		return intentSource(t, source, edits.ParamWriterAuto, &edits.RemoveSceneSetting{Entity: entity})
	}
	return intentSource(t, source, edits.ParamWriterAuto, &edits.SetSceneSetting{Entity: entity, Value: value})
}

func intentProjectSettings(t *testing.T, source []byte, settings *studioProjectSettings) ([]byte, error) {
	intent := &edits.SetProjectSettings{}
	if settings != nil {
		intent.Title, intent.TempoMilli, intent.Root, intent.Scale = settings.Title, settings.TempoMilli, settings.Root, settings.Scale
	}
	return intentSource(t, source, edits.ParamWriterAuto, intent)
}

func intentClipSettings(t *testing.T, source []byte, id string, settings *studioClipSettings) ([]byte, error) {
	intent := &edits.SetClipSettings{Entity: edits.EntityID("clip:" + id)}
	if settings != nil {
		intent.Start, intent.End, intent.GainDB, intent.FadeIn, intent.FadeOut = settings.Start, settings.End, settings.GainDB, settings.FadeIn, settings.FadeOut
	}
	return looseIntentSource(t, source, edits.ParamWriterAuto, intent)
}

func intentAudioTrack(t *testing.T, source []byte, name string) ([]byte, error) {
	return looseIntentSource(t, source, edits.ParamWriterAuto, &edits.AddAudioTrack{Name: name})
}

func intentAddPreset(t *testing.T, source []byte, preset, instrument, track string) ([]byte, error) {
	return looseIntentSource(t, source, edits.ParamWriterAuto, &edits.AddPreset{Preset: preset, Instrument: instrument, Track: track})
}

// intentInstrumentParameter sets an instrument parameter; an empty reset
// value restores the default. A value that is a JSON number stays a number.
func intentInstrumentParameter(t *testing.T, source []byte, track, parameter, value string, reset bool) ([]byte, error) {
	intent := &edits.SetParam{Entity: edits.EntityID("param:" + track + "." + parameter)}
	if !reset {
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			intent.Value = json.RawMessage(value)
		} else {
			intent.Value, _ = json.Marshal(value)
		}
	}
	return intentSource(t, source, edits.ParamWriterInstrument, intent)
}

func intentBindScene(t *testing.T, source []byte, scene, track, pattern string) ([]byte, error) {
	return looseIntentSource(t, source, edits.ParamWriterAuto, &edits.BindScene{Scene: scene, Track: track, Pattern: pattern})
}

func intentPatternSettings(t *testing.T, source []byte, id string, settings *studioPatternSettings) ([]byte, error) {
	intent := &edits.SetPatternSettings{Entity: edits.EntityID("pattern:" + id)}
	if settings != nil {
		intent.Swing100, intent.Gate, intent.Transpose = settings.Swing100, settings.Gate, settings.Transpose
	}
	return intentSource(t, source, edits.ParamWriterAuto, intent)
}

func intentPatternStep(t *testing.T, source []byte, id, lane string, index int, note *studioStepEdit) ([]byte, error) {
	intent := &edits.SetStep{Entity: intentStepEntity(id, lane, index), Shared: "pattern"}
	if note != nil {
		intent.Mode, intent.Pitch, intent.Accent, intent.Slide, intent.Ratchet, intent.Chance, intent.Velocity = note.Mode, note.Pitch, note.Accent, note.Slide, note.Ratchet, note.Chance, note.Velocity
	}
	return intentSource(t, source, edits.ParamWriterAuto, intent)
}

func intentDuplicatePattern(t *testing.T, source []byte, id, name string) ([]byte, error) {
	return intentSource(t, source, edits.ParamWriterAuto, &edits.DuplicatePattern{Entity: edits.EntityID("pattern:" + id), Name: name})
}

func intentPatternRange(t *testing.T, source []byte, id, lane string, r *studioPatternRange) ([]byte, error) {
	intent := &edits.SetRange{Entity: edits.EntityID("pattern:" + id), Lane: lane, First: -1, Shared: "pattern"}
	if r != nil {
		intent.Operation, intent.First, intent.Last, intent.Target, intent.Amount = r.Operation, r.First, r.Last, r.Target, r.Amount
	}
	return intentSource(t, source, edits.ParamWriterAuto, intent)
}

func intentResizePattern(t *testing.T, source []byte, id string, length int) ([]byte, error) {
	return intentSource(t, source, edits.ParamWriterAuto, &edits.ResizePattern{Entity: edits.EntityID("pattern:" + id), Length: length, Shared: "pattern"})
}

// intentSavedPresetSource builds the saved-preset declaration in the host, as
// the route does, and applies it with the SavePreset intent.
func intentSavedPresetSource(t *testing.T, scorePath string, source []byte, track, name string) ([]byte, error) {
	declaration, err := studioSavedPresetDeclaration(scorePath, source, track, name)
	if err != nil {
		return nil, err
	}
	result, err := edits.Apply(source, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SavePreset{Track: track, Name: name, Declaration: string(declaration)}}}, edits.Options{Compiler: &studioCompiler{path: scorePath}})
	if err != nil {
		return nil, err
	}
	return result.Source, nil
}

// testDrumToken returns the authored token of one drum lane step.
func testDrumToken(source []byte, pattern, lane string, index int) (string, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return "", fmt.Errorf("score must validate")
	}
	for _, p := range score.Patterns {
		if p.Name != pattern || p.Kind != "drums" {
			continue
		}
		for _, l := range p.Lanes {
			if l.Name == lane && index >= 0 && index < len(l.Hits) {
				return l.Hits[index].Text, nil
			}
		}
	}
	return "", fmt.Errorf("pattern %s has no drum lane %s", pattern, lane)
}

// testDeclarationText returns the source text of one named pattern declaration.
func testDeclarationText(t *testing.T, source []byte, name string) string {
	t.Helper()
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		switch walker.Type(node) {
		case "acid_pattern", "note_pattern", "drum_pattern":
			if walker.Text(walker.Field(node, "name")) == name {
				return string(source[node.StartByte():node.EndByte()])
			}
		}
	}
	t.Fatalf("pattern %q is missing", name)
	return ""
}
