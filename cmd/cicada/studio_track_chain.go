package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

// addVocalChain creates and attaches three independently editable declarations
// in one revision-checked, undoable transaction. Existing inserts keep order.
func (s *studio) addVocalChain(w http.ResponseWriter, request studioEdit) {
	if !edits.ValidMixerIdentifier(request.Track) {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "choose a track for the vocal chain"})
		return
	}
	request.Label = "Add vocal chain to " + request.Track
	var chain []string
	var response map[string]any
	prepare := func(source []byte, opts *edits.Options) (edits.Envelope, error) {
		if s.captureID != "" {
			return edits.Envelope{}, fmt.Errorf("finish the recording before adding vocal effects")
		}
		score, ds, err := parseScoreForPath(s.path, source)
		if err != nil {
			return edits.Envelope{}, err
		}
		if score == nil || hasDiagnosticErrors(ds) {
			return edits.Envelope{}, fmt.Errorf("score must validate before adding vocal effects")
		}
		score, ds = notation.ResolvePresets(score)
		if hasDiagnosticErrors(ds) {
			return edits.Envelope{}, fmt.Errorf("presets must validate before adding vocal effects")
		}
		var track *notation.Track
		used := map[string]bool{}
		for i := range score.Tracks {
			t := &score.Tracks[i]
			used[t.Name] = true
			if t.Name == request.Track {
				track = t
			}
		}
		for _, effect := range score.Effects {
			used[effect.Name] = true
		}
		for _, bus := range score.Buses {
			used[bus.Name] = true
		}
		used["music"], used["sfx"], used["master"] = true, true, true
		if track == nil {
			return edits.Envelope{}, fmt.Errorf("unknown track %s", request.Track)
		}
		chain = sourceInsertNames(track.Params)
		// Any existing EQ -> compressor -> de-esser trio fulfils this preset. A
		// second click must never double compression or silently change settings.
		for i := 0; i+2 < len(chain); i++ {
			a, b, c := findNotationEffect(score, chain[i]), findNotationEffect(score, chain[i+1]), findNotationEffect(score, chain[i+2])
			if a != nil && b != nil && c != nil && a.Kind == "eq" && b.Kind == "comp" && c.Kind == "deesser" {
				response = map[string]any{"track": request.Track, "inserts": chain, "message": "Vocal chain is already present."}
				return edits.Envelope{}, nil
			}
		}
		if len(chain)+3 > engine.MaxTrackInserts {
			return edits.Envelope{}, fmt.Errorf("vocal chain needs three free insert slots; tracks support eight stages")
		}
		name := func(suffix string) string {
			base := request.Track
			if len(base) > 42 {
				base = base[:42]
			}
			base += "_" + suffix
			candidate := base
			for n := 2; used[candidate]; n++ {
				candidate = base + "_" + strconv.Itoa(n)
			}
			used[candidate] = true
			return candidate
		}
		hp, comp, deess := name("vocal_hp"), name("vocal_comp"), name("vocal_deess")
		var intents []edits.Intent
		for _, spec := range []struct{ name, kind string }{{hp, "eq"}, {comp, "comp"}, {deess, "deesser"}} {
			intents = append(intents, &edits.AddEffect{Name: spec.name, EffectKind: spec.kind, ConfirmUpgrade: request.ConfirmUpgrade})
		}
		set := func(path string, value any) {
			raw, _ := json.Marshal(value)
			intents = append(intents, &edits.SetParam{Entity: edits.EntityID("param:" + path), Value: raw, ConfirmUpgrade: request.ConfirmUpgrade})
		}
		set(hp+".type", "highpass")
		set(hp+".frequency", "80Hz")
		set(hp+".q", .707)
		set(comp+".threshold", "-18dB")
		set(comp+".ratio", 3)
		set(comp+".makeup", "0dB")
		chain = append(chain, hp, comp, deess)
		set(request.Track+".insert", strings.Join(chain, " -> "))
		opts.ParamWriter = edits.ParamWriterMixer
		response = map[string]any{"track": request.Track, "inserts": chain}
		return edits.Envelope{Intents: intents}, nil
	}
	finish := func(result *edits.Result) error {
		result.Response = response
		return nil
	}
	s.applyPreparedIntents(w, request, edits.Envelope{}, edits.ParamWriterAuto, prepare, finish, nil)
}
