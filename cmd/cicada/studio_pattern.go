package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	edits "m31labs.dev/cicada/edit"
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
	case "chord":
		if edit.Chord == nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose chord pitches and a hold"})
			return
		}
		in := *edit.Chord
		in.Entity, in.Shared = studioStepEntity(edit), "pattern"
		intent = &in
		edit.Label = fmt.Sprintf("Pattern · %s step %d chord · %d steps", edit.Pattern, edit.Step+1, in.Hold)
	case "create":
		intent = &edits.CreatePattern{Name: edit.NewName, Track: edit.Track, Steps: edit.Length, Scene: edit.Scene}
		edit.Label = fmt.Sprintf("Pattern · %s created for %s", edit.NewName, edit.Track)
	case "performance-group":
		if edit.PerformanceGroup == nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "a shared gesture lane is required"})
			return
		}
		in := *edit.PerformanceGroup
		in.Entity = entity
		intent = &in
	case "performance-binding":
		if edit.PerformanceBinding == nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "a note and shared gesture lane are required"})
			return
		}
		in := *edit.PerformanceBinding
		in.Entity = entity
		intent = &in
	case "shared-gesture":
		if edit.SharedExpression == nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "a shared gesture point is required"})
			return
		}
		in := *edit.SharedExpression
		in.Entity = entity
		intent = &in
	case "performed-gesture":
		if edit.PerformanceExpression == nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "a performed gesture sample is required"})
			return
		}
		in := *edit.PerformanceExpression
		in.Entity = entity
		intent = &in
	case "performed-note":
		if edit.PerformanceNote == nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "a performed note is required"})
			return
		}
		in := *edit.PerformanceNote
		in.Entity = entity
		intent = &in
	case "performance-length":
		intent = &edits.ResizePerformance{Entity: entity, LengthTicks: edit.PerformanceLength}
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

var studioPatternName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,63}$`)
