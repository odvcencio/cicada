package main

import (
	"net/http"

	edits "m31labs.dev/cicada/edit"
)

type studioProjectSettings struct {
	Title      string `json:"title"`
	TempoMilli int    `json:"tempoMilli"`
	Root       string `json:"root"`
	Scale      string `json:"scale"`
}

func (s *studio) editProject(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	edit.Action = "project"
	intent := &edits.SetProjectSettings{}
	if settings := edit.Metadata; settings != nil {
		intent.Title, intent.TempoMilli, intent.Root, intent.Scale = settings.Title, settings.TempoMilli, settings.Root, settings.Scale
	}
	edit.Label = studioEditLabel(edit)
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil)
}
