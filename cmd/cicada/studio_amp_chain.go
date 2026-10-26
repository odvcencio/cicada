package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/notation"
)

// applyAmpChain commits a track-local reamp insert and its controls together.
func (s *studio) applyAmpChain(w http.ResponseWriter, request studioEdit) {
	if !edits.ValidMixerIdentifier(request.Track) || request.AmpDrive == nil || request.AmpTone == nil || request.AmpOutput == nil || request.AmpCabinet == nil {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "choose an audio track and provide all amp controls"})
		return
	}
	params := ampcab.Params{Drive: *request.AmpDrive, Tone: *request.AmpTone, Output: *request.AmpOutput, Cabinet: *request.AmpCabinet}
	if err := params.Validate(); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	request.Label = "Apply amp to " + request.Track
	var response map[string]any
	prepare := func(source []byte, opts *edits.Options) (edits.Envelope, error) {
		if s.captureID != "" {
			return edits.Envelope{}, fmt.Errorf("finish the recording before applying amp settings")
		}
		score, ds, err := parseScoreForPath(s.path, source)
		if err != nil {
			return edits.Envelope{}, err
		}
		if score == nil || hasDiagnosticErrors(ds) {
			return edits.Envelope{}, fmt.Errorf("score must validate before applying amp settings")
		}
		score, ds = notation.ResolvePresets(score)
		if hasDiagnosticErrors(ds) {
			return edits.Envelope{}, fmt.Errorf("presets must validate before applying amp settings")
		}
		var track *notation.Track
		used := map[string]bool{"music": true, "sfx": true, "master": true}
		for i := range score.Tracks {
			t := &score.Tracks[i]
			used[t.Name] = true
			if t.Name == request.Track {
				track = t
			}
		}
		for _, fx := range score.Effects {
			used[fx.Name] = true
		}
		for _, bus := range score.Buses {
			used[bus.Name] = true
		}
		if track == nil || track.Kind != "audio" {
			return edits.Envelope{}, fmt.Errorf("choose an existing audio track")
		}
		chain := sourceInsertNames(track.Params)
		base := request.Track
		if len(base) > 42 {
			base = base[:42]
		}
		base += "_live_amp"
		name := ""
		for _, entry := range chain {
			if entry == base || strings.HasPrefix(entry, base+"_") {
				if fx := findNotationEffect(score, entry); fx != nil && fx.Kind == "ampcab" {
					name = entry
					break
				}
			}
		}
		if name != "" {
			for _, other := range score.Tracks {
				if other.Name == track.Name {
					continue
				}
				for _, entry := range sourceInsertNames(other.Params) {
					if entry == name {
						return edits.Envelope{}, fmt.Errorf("amp effect %s is shared by another track; give this track its own effect before applying", name)
					}
				}
			}
		}
		var intents []edits.Intent
		if name == "" {
			if len(chain) >= engine.MaxTrackInserts {
				return edits.Envelope{}, fmt.Errorf("amp needs one free insert slot")
			}
			name = base
			for n := 2; used[name]; n++ {
				name = base + "_" + strconv.Itoa(n)
			}
			intents = append(intents, &edits.AddEffect{Name: name, EffectKind: "ampcab", ConfirmUpgrade: request.ConfirmUpgrade})
			chain = append([]string{name}, chain...)
		}
		set := func(path string, value any) {
			raw, _ := json.Marshal(value)
			intents = append(intents, &edits.SetParam{Entity: edits.EntityID("param:" + path), Value: raw, ConfirmUpgrade: request.ConfirmUpgrade})
		}
		set(name+".drive", params.Drive)
		set(name+".tone", params.Tone)
		set(name+".output", fmt.Sprintf("%gdB", params.Output))
		set(name+".cabinet", params.Cabinet)
		set(name+".bypass", "off")
		set(request.Track+".insert", strings.Join(chain, " -> "))
		opts.ParamWriter = edits.ParamWriterMixer
		response = map[string]any{"track": request.Track, "effect": name, "inserts": chain}
		return edits.Envelope{Intents: intents}, nil
	}
	finish := func(result *edits.Result) error { result.Response = response; return nil }
	s.applyPreparedIntents(w, request, edits.Envelope{}, edits.ParamWriterAuto, prepare, finish, nil)
}
