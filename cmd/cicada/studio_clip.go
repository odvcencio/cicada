package main

import (
	"bytes"
	"fmt"
	"net/http"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/notation"
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

func newAudioTrackSource(source []byte, name string, edition int) ([]byte, error) {
	if edition != 2 || !studioPatternName.MatchString(name) {
		return nil, fmt.Errorf("choose a lowercase audio track name in an edition-2 score")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	if root.HasError() {
		return nil, fmt.Errorf("fix score syntax before adding a track")
	}
	// Imported instruments are resolved by the enclosing project transaction.
	// Inspect local declarations without trying to validate a loose-file score.
	tracks := 0
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) != "track_decl" {
			continue
		}
		tracks++
		if walker.Text(walker.Field(node, "name")) == name {
			return nil, fmt.Errorf("track %s already exists", name)
		}
	}
	if tracks >= limits.MaxTracks {
		return nil, fmt.Errorf("a project supports at most %d tracks", limits.MaxTracks)
	}
	at := len(source)
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "scene_decl" {
			at = int(node.StartByte())
			break
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	text := "track " + name + " audio {}" + newline + newline
	if at > 0 && source[at-1] != '\n' {
		text = newline + text
	}
	return replaceSongSpan(source, at, at, []byte(text))
}
