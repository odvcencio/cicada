package main

import (
	"net/http"

	edits "m31labs.dev/cicada/edit"
)

type studioPlaceSettings struct {
	At      string `json:"at,omitempty"`
	Length  string `json:"length,omitempty"`
	Track   string `json:"track,omitempty"`
	Content string `json:"content,omitempty"`
}

func (s *studio) editArrangement(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	intent, ok := arrangementIntent(edit)
	if !ok {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown placement action"})
		return
	}
	edit.Label = studioEditLabel(edit)
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil)
}

// arrangementIntent maps a placement request to its intent. The second result
// is false for an unknown action.
func arrangementIntent(edit studioEdit) (edits.Intent, bool) {
	var settings *edits.PlacementSettings
	if edit.Placement != nil {
		settings = &edits.PlacementSettings{At: edit.Placement.At, Length: edit.Placement.Length, Track: edit.Placement.Track, Content: edit.Placement.Content}
	}
	placement, marker := edits.EntityID("placement:"+edit.Name), edits.EntityID("marker:"+edit.Name)
	switch edit.Action {
	case "update":
		return &edits.UpdatePlacement{Entity: placement, Settings: settings}, true
	case "append":
		return &edits.AddPlacement{Name: edit.NewName, Settings: settings}, true
	case "duplicate":
		return &edits.CopyPlacement{Entity: placement, Name: edit.NewName, Settings: settings}, true
	case "delete":
		return &edits.RemovePlacement{Entity: placement}, true
	case "marker":
		return &edits.SetMarker{Entity: marker, Settings: settings}, true
	case "append-marker":
		return &edits.AddMarker{Name: edit.NewName, Settings: settings}, true
	case "delete-marker":
		return &edits.RemoveMarker{Entity: marker}, true
	}
	return nil, false
}
