package main

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

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
	s.apply(w, edit, func(source []byte) ([]byte, error) {
		switch edit.Action {
		case "audio-track":
			return newAudioTrackSource(source, edit.NewName, edition)
		case "clip-settings":
			return clipSettingsSource(source, edit.Pattern, edit.ClipSettings, edition)
		default:
			return bindPatternSource(source, edit.Scene, edit.Track, edit.Pattern)
		}
	})
}

func newAudioTrackSource(source []byte, name string, edition int) ([]byte, error) {
	if edition != 2 || !studioPatternName.MatchString(name) {
		return nil, fmt.Errorf("choose a lowercase audio track name in an edition-2 score")
	}
	score, ds := notation.ParseEdition(source, edition)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before adding a track")
	}
	for _, track := range score.Tracks {
		if track.Name == name {
			return nil, fmt.Errorf("track %s already exists", name)
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

func clipSettingsSource(source []byte, id string, settings *studioClipSettings, edition int) ([]byte, error) {
	if settings == nil || settings.Start < 0 || settings.End <= settings.Start || settings.FadeIn < 0 || settings.FadeOut < 0 || settings.FadeIn > settings.End-settings.Start || settings.FadeOut > settings.End-settings.Start || math.IsNaN(settings.GainDB) || math.IsInf(settings.GainDB, 0) || settings.GainDB < -60 || settings.GainDB > 24 {
		return nil, fmt.Errorf("choose a valid source region, fades no longer than the region, and gain −60 to +24 dB")
	}
	score, ds := notation.ParseEdition(source, edition)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before editing audio")
	}
	var original *notation.Clip
	for i := range score.Clips {
		if score.Clips[i].Name == id {
			original = &score.Clips[i]
			break
		}
	}
	if original == nil {
		return nil, fmt.Errorf("unknown clip %s", id)
	}
	for _, asset := range score.Assets {
		if asset.Name == original.Asset && settings.End > asset.Frames {
			return nil, fmt.Errorf("clip end exceeds the source asset")
		}
	}
	node, walker, err := studioDeclaration(source, []string{"clip_decl"}, id)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, value := range []struct {
		name      string
		old, next int64
	}{{"start", original.StartFrame, settings.Start}, {"end", original.EndFrame, settings.End}, {"fade_in", original.FadeInFrames, settings.FadeIn}, {"fade_out", original.FadeOutFrames, settings.FadeOut}} {
		if value.old != value.next {
			values[value.name] = strconv.FormatInt(value.next, 10) + "frames"
		}
	}
	if original.GainDB != settings.GainDB {
		values["gain"] = strconv.FormatFloat(settings.GainDB, 'f', -1, 64) + "dB"
	}
	var edits []studioSpanEdit
	for i := 0; i < node.NamedChildCount(); i++ {
		param := node.NamedChild(i)
		if walker.Type(param) != "param_decl" {
			continue
		}
		name := walker.Text(walker.Field(param, "name"))
		if text, ok := values[name]; ok {
			value := walker.Field(param, "value")
			edits = append(edits, studioSpanEdit{int(value.StartByte()), int(value.EndByte()), text})
			delete(values, name)
		}
	}
	var fields []string
	for _, name := range []string{"start", "end", "gain", "fade_in", "fade_out"} {
		if text, ok := values[name]; ok {
			fields = append(fields, name+" = "+text)
		}
	}
	if len(fields) > 0 {
		at := int(node.StartByte()) + bytes.IndexByte(source[node.StartByte():node.EndByte()], '{') + 1
		edits = append(edits, studioSpanEdit{at, at, " " + strings.Join(fields, " ") + " "})
	}
	return patchStudioSpans(source, edits)
}
