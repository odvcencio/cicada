package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func (s *studio) editAutomation(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Action == "automation-point-set" || edit.Action == "automation-point-remove" {
		edition, err := mixerSourceEdition(s.path)
		if err != nil || edition < 2 {
			studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "continuous automation needs an edition-2 score"})
			return
		}
		var point studioCurvePoint
		if err := json.Unmarshal(edit.Value, &point); err != nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose an automation point"})
			return
		}
		s.apply(w, edit, func(source []byte) ([]byte, error) {
			if s.captureID != "" {
				return nil, fmt.Errorf("finish the recording before editing automation")
			}
			return curvePointSource(source, edit.Path, point, edit.Action == "automation-point-remove", edition)
		})
		return
	}
	if edit.Action != "automation-set" && edit.Action != "automation-remove" {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose an automation operation"})
		return
	}
	_, err := mixerSourceEdition(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	entity := edits.EntityID("setting:" + edit.Scene + "/" + edit.Path)
	var intent edits.Intent = &edits.SetSceneSetting{Entity: entity, Value: edit.Value}
	if edit.Action == "automation-remove" {
		intent = &edits.RemoveSceneSetting{Entity: entity}
	}
	edit.Label = studioEditLabel(edit)
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil)
}

// Scene points use the existing parameter catalog and playback/render glides.
// Only the assignment's value or syntax span changes; surrounding comments,
// bindings, authored whitespace, and newline convention remain intact.
func sceneAutomationSource(source []byte, sceneID, path string, raw json.RawMessage, remove bool, edition int) ([]byte, error) {
	score, ds := notation.ParseEdition(source, edition)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before editing automation")
	}
	p, ds := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score parameters must validate before editing automation")
	}
	resolved, err := project.ResolveParameterPath(p, path)
	if err != nil {
		return nil, err
	}
	// The scene tempo is the one bare path a scene may set. It is not live and
	// not automatable: it steps at the scene boundary.
	tempo := path == "tempo" && resolved.Descriptor.ID == "global.tempo"
	if !tempo && (!strings.Contains(path, ".") || !resolved.Descriptor.Live || !remove && !resolved.Descriptor.Automatable) {
		return nil, fmt.Errorf("%s cannot be automated at a scene boundary", path)
	}
	node, walker, err := studioDeclaration(source, []string{"scene_decl"}, sceneID)
	if err != nil {
		return nil, err
	}
	literal := ""
	if !remove {
		descriptor := resolved.Descriptor
		// Display steps guide controls, but an authored automation value keeps
		// its full precision within the shared parameter range.
		descriptor.DisplayStep = 0
		literal, err = canonicalMixerLiteral(descriptor, raw)
		if err != nil {
			return nil, err
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		assignment := node.NamedChild(i)
		if walker.Type(assignment) != "scene_assignment" || walker.Text(walker.Field(assignment, "target")) != path || tempo && !notation.SceneAssignmentIsSetting(path, walker.Text(walker.Field(assignment, "value"))) {
			continue
		}
		if remove {
			return replaceSongSpan(source, int(assignment.StartByte()), int(assignment.EndByte()), nil)
		}
		value := walker.Field(assignment, "value")
		return replaceSongSpan(source, int(value.StartByte()), int(value.EndByte()), []byte(literal))
	}
	if remove {
		return nil, fmt.Errorf("scene %s has no point for %s", sceneID, path)
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(node.StartByte()) + bytes.IndexByte(source[node.StartByte():node.EndByte()], '{') + 1
	return replaceSongSpan(source, at, at, []byte(newline+"  "+path+" = "+literal+newline))
}
