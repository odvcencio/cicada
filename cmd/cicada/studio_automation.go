package main

import (
	"encoding/json"
	"fmt"
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
