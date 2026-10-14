package main

import (
	"net/http"

	edits "m31labs.dev/cicada/edit"
)

type studioClipSettings struct {
	Start   int64   `json:"start"`
	End     int64   `json:"end"`
	GainDB  float64 `json:"gainDB"`
	FadeIn  int64   `json:"fadeIn"`
	FadeOut int64   `json:"fadeOut"`
}

func (s *studio) editClip(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	edition, err := mixerSourceEdition(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if edition != 2 {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Audio tracks require edition 2. Use the Mixer upgrade option first."})
		return
	}
	if edit.Action != "audio-track" && edit.Action != "clip-settings" && edit.Action != "clip-bind" {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown audio clip action"})
		return
	}
	var intent edits.Intent
	switch edit.Action {
	case "audio-track":
		intent = &edits.AddAudioTrack{Name: edit.NewName}
	case "clip-settings":
		settings := &edits.SetClipSettings{Entity: edits.EntityID("clip:" + edit.Pattern)}
		if original := edit.ClipSettings; original != nil {
			settings.Start, settings.End, settings.GainDB, settings.FadeIn, settings.FadeOut = original.Start, original.End, original.GainDB, original.FadeIn, original.FadeOut
		}
		intent = settings
	default:
		intent = &edits.BindScene{Scene: edit.Scene, Track: edit.Track, Pattern: edit.Pattern}
	}
	edit.Label = studioEditLabel(edit)
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil)
}
