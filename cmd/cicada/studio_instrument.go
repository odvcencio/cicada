package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

func (s *studio) editInstrument(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Action != "add-preset" && edit.Action != "set-parameter" && edit.Action != "reset-parameter" {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown instrument action"})
		return
	}
	if edit.Action == "set-parameter" || edit.Action == "reset-parameter" {
		if edit.Action == "set-parameter" {
			// Keep the writer's early refusal: it answers before the revision check,
			// and an absent value would otherwise read as a reset.
			var text string
			if err := json.Unmarshal(edit.Value, &text); err != nil {
				var number json.Number
				if err := json.Unmarshal(edit.Value, &number); err != nil || number == "" {
					studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "parameter value must be a number or compatible unit literal"})
					return
				}
			}
		}
		intent := &edits.SetParam{Entity: edits.EntityID("param:" + edit.Track + "." + edit.NewName)}
		if edit.Action == "set-parameter" {
			intent.Value = edit.Value
		}
		s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterInstrument, nil)
		return
	}
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{&edits.AddPreset{Preset: edit.Pattern, Instrument: edit.NewName, Track: edit.Track}}}, edits.ParamWriterAuto, nil)
}

func instrumentParameterSource(source []byte, score *notation.Score, trackID, parameterID, value string, reset bool) ([]byte, error) {
	var definition *notation.Instrument
	for _, track := range score.Tracks {
		if track.Name != trackID {
			continue
		}
		for i := range score.Instruments {
			if score.Instruments[i].Name == track.Kind {
				definition = &score.Instruments[i]
				break
			}
		}
		break
	}
	if definition == nil {
		return nil, fmt.Errorf("track %q has no authored instrument", trackID)
	}
	program, ds := instrument.Compile(*definition)
	if program == nil || hasDiagnosticErrors(ds) || !program.HasParameter(parameterID) {
		return nil, fmt.Errorf("instrument %s does not declare parameter %q", definition.Name, parameterID)
	}
	literal := ""
	if !reset {
		var err error
		literal, err = instrument.ParameterLiteral(program, parameterID, value)
		if err != nil {
			return nil, err
		}
	}
	decl, walker, err := studioDeclaration(source, []string{"track_decl"}, trackID)
	if err != nil {
		return nil, err
	}
	for i := 0; i < decl.NamedChildCount(); i++ {
		parameter := decl.NamedChild(i)
		if walker.Type(parameter) == "mix_setting" && parameter.NamedChildCount() > 0 {
			parameter = parameter.NamedChild(0)
		}
		if walker.Type(parameter) != "param_decl" || walker.Text(walker.Field(parameter, "name")) != parameterID {
			continue
		}
		if reset {
			start, end := int(parameter.StartByte()), int(parameter.EndByte())
			text := source[start:end]
			if bytes.Contains(text, []byte("//")) || bytes.Contains(text, []byte("/*")) {
				return nil, fmt.Errorf("this override contains a comment; remove it in Score to preserve the annotation")
			}
			return replaceSongSpan(source, start, end, nil)
		}
		node := walker.Field(parameter, "value")
		return replaceSongSpan(source, int(node.StartByte()), int(node.EndByte()), []byte(literal))
	}
	if reset {
		return bytes.Clone(source), nil
	}
	at := int(decl.StartByte()) + bytes.IndexByte(source[decl.StartByte():decl.EndByte()], '{') + 1
	return replaceSongSpan(source, at, at, []byte(" "+parameterID+" = "+literal+" "))
}

// addPresetSource inserts a complete authored graph and its track in one edit.
// The normal Studio compiler checks DSP bounds and the full voice budget before
// s.apply can publish it, including the existing tracks in the score.
func addPresetSource(source []byte, presetID, instrumentName, trackName string) ([]byte, error) {
	score, ds := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before adding an instrument")
	}
	return addPresetSourceParsed(source, presetID, instrumentName, trackName, score)
}

func addPresetSourceParsed(source []byte, presetID, instrumentName, trackName string, score *notation.Score) ([]byte, error) {
	patch, ok := instrument.FindPatch(presetID)
	if !ok {
		return nil, fmt.Errorf("choose a patch from the instrument library")
	}
	declaration, err := patch.Source(instrumentName)
	if err != nil {
		return nil, err
	}
	if !studioPatternName.MatchString(trackName) {
		return nil, fmt.Errorf("choose a lowercase track name")
	}
	for _, existing := range score.Instruments {
		if existing.Name == instrumentName {
			return nil, fmt.Errorf("instrument %s already exists", instrumentName)
		}
	}
	for _, existing := range score.Samplers {
		if existing.Name == instrumentName {
			return nil, fmt.Errorf("sampler %s already exists", instrumentName)
		}
	}
	for _, existing := range score.Kits {
		if existing.Name == instrumentName {
			return nil, fmt.Errorf("kit %s already exists", instrumentName)
		}
	}
	for _, existing := range score.Tracks {
		if existing.Name == trackName {
			return nil, fmt.Errorf("track %s already exists", trackName)
		}
	}
	if len(score.Tracks) >= 16 {
		return nil, fmt.Errorf("a project supports at most 16 tracks")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	at := len(source)
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if kind := walker.Type(node); kind == "scene_decl" || kind == "song_decl" {
			at = int(node.StartByte())
			break
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	text := strings.ReplaceAll(declaration+"\ntrack "+trackName+" "+instrumentName+" {}\n\n", "\n", newline)
	if at > 0 && source[at-1] != '\n' {
		text = newline + text
	}
	return replaceSongSpan(source, at, at, []byte(text))
}
