package main

import (
	"encoding/json"
	"net/http"

	edits "m31labs.dev/cicada/edit"
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
		env := edits.Envelope{Intents: []edits.Intent{curvePointIntent(edit.Path, point, edit.Action == "automation-point-remove")}}
		edit.Label = studioEditLabel(edit)
		s.applyPreparedIntents(w, edit, env, edits.ParamWriterAuto, func([]byte, *edits.Options) (edits.Envelope, error) {
			if err := s.automationCaptureGuard(); err != nil {
				return edits.Envelope{}, err
			}
			return env, nil
		}, nil, nil)
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

// curvePointIntent maps one automation point request to its intent.
func curvePointIntent(path string, point studioCurvePoint, remove bool) edits.Intent {
	entity := edits.EntityID("param:" + path)
	if remove {
		return &edits.RemoveAutomationPoint{Entity: entity, OldAt: point.OldAt, At: point.At, Shape: point.Shape, Curve: point.Curve}
	}
	return &edits.SetAutomationPoint{Entity: entity, OldAt: point.OldAt, At: point.At, Value: point.Value, Shape: point.Shape, Curve: point.Curve}
}
