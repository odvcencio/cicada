package main

import (
	"encoding/json"
	"net/http"

	edits "m31labs.dev/cicada/edit"
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
