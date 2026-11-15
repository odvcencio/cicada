package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
)

func (s *studioApp) addEffect(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	if f["revision"] == "" || f["source_revision"] == "" {
		return action.Error(http.StatusConflict, "Refresh Studio before adding an effect.")
	}
	if _, known := effectKindLabels[f["effectkind"]]; !known {
		return action.Validation("Choose an effect kind.", nil, f)
	}
	name := strings.TrimSpace(f["name"])
	if name == "" {
		// An empty name asks Studio to name the effect after its kind.
		var mixer mixerState
		if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/mixer", nil, &mixer); err != nil {
			var failure *backendError
			refusal := action.Error(http.StatusServiceUnavailable, "Could not read the mixer to name the effect. Enter a name and try again.")
			if errors.As(err, &failure) {
				refusal = action.Error(failure.Status, failure.Message)
			}
			refusal.Result.Values = f
			return refusal
		}
		name = freeEffectName(mixer, f["effectkind"])
	}
	if !edit.ValidMixerIdentifier(name) {
		return action.Validation("Enter a valid effect name using lowercase letters, digits, underscores or hyphens (at most 64 characters).", nil, f)
	}
	in := &edit.AddEffect{Name: name, EffectKind: f["effectkind"], InsertOn: f["inserton"], ConfirmUpgrade: f["confirmUpgrade"] == "on"}
	if f["effectkind"] == "convolution" && f["asset"] != "" {
		b, _ := json.Marshal(f["asset"])
		in.Settings = map[string]json.RawMessage{"asset": b}
	}
	env := edit.Envelope{Version: edit.EnvelopeVersion, Revision: f["revision"], Intents: []edit.Intent{in}}
	var result any
	err := s.backend.call(context.WithValue(ctx.Request.Context(), expectedProjectSourcesKey{}, f["source_revision"]), http.MethodPost, "/api/intents", env, &result)
	if err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			refusal := action.Error(failure.Status, failure.Message)
			refusal.Result.Values = f
			var body map[string]any
			if json.Unmarshal(failure.Data, &body) == nil {
				if reason, ok := body["reason"].(string); ok {
					refusal.Result.Data, _ = json.Marshal(map[string]string{"reason": reason})
				}
			}
			return refusal
		}
		refusal := action.Error(http.StatusServiceUnavailable, "Could not confirm whether the effect was added. Refresh saved state before retrying.")
		refusal.Result.Values = f
		return refusal
	}
	ctx.FormData = nil
	ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/?panel=mixer"), "Effect added.")
	return nil
}

// effectKindLabels names each effect kind the way a mixer does. Its keys are
// the kinds the add-effect action accepts.
var effectKindLabels = map[string]string{
	"drive": "Drive", "eq": "EQ", "comp": "Compressor", "deesser": "De-esser", "ampcab": "Amp and cabinet", "chorus": "Chorus",
	"transient": "Transient shaper", "width": "Stereo width", "limiter": "Limiter", "convolution": "Convolution reverb",
	"delay": "Delay", "tape": "Tape delay", "reverb": "Reverb",
}

// freeEffectName returns the kind, or the kind with the first number that no
// channel or effect in the mixer uses.
func freeEffectName(mixer mixerState, kind string) string {
	taken := map[string]bool{"music": true, "sfx": true, "master": true}
	for _, strips := range [][]mixerStrip{mixer.Tracks, mixer.Buses} {
		for _, strip := range strips {
			taken[strip.ID], taken[strip.Name] = true, true
		}
	}
	for _, effects := range [][]mixerReturn{mixer.Returns, mixer.Effects} {
		for _, effect := range effects {
			taken[effect.Name] = true
		}
	}
	first := kind == "reverb"
	for _, effect := range mergeMixerReturns(mixer.Returns, mixer.Effects) {
		if effect.Kind == "reverb" {
			masterInsert := false
			for _, name := range mixer.Master.Inserts {
				masterInsert = masterInsert || name == effect.Name
			}
			if !masterInsert {
				first = false
			}
		}
	}
	name := kind
	for i := 2; taken[name] || paramdefs.ReturnNameCollision(name, kind, first) != ""; i++ {
		name = fmt.Sprintf("%s%d", kind, i)
	}
	return name
}

// effectFields are the kind and name fields of an add-effect form. The name is
// optional: the action names the effect after its kind when the field is empty.
func effectFields(kinds []string) []gosx.Node {
	var options []gosx.Node
	for i, kind := range kinds {
		attrs := gosx.Attrs(gosx.Attr("value", kind))
		if i == 0 {
			attrs = append(attrs, gosx.BoolAttr("selected"))
		}
		options = append(options, gosx.El("option", attrs, gosx.Text(effectKindLabels[kind])))
	}
	return []gosx.Node{
		field("Kind", gosx.El("select", gosx.Attrs(gosx.Attr("name", "effectkind")), gosx.Fragment(options...))),
		field("Name (optional)", gosx.El("input", gosx.Attrs(gosx.Attr("name", "name"), gosx.Attr("value", ""), gosx.Attr("placeholder", "named after its kind"), gosx.Attr("autocomplete", "off")))),
	}
}

func insertKinds(c mixerChannel) []string {
	switch c.Kind {
	case "track":
		return []string{"drive", "eq", "comp", "deesser", "ampcab", "chorus"}
	case "bus":
		if c.ID == "music" {
			return []string{"comp"}
		}
	case "master":
		return []string{"eq", "comp", "transient", "width", "limiter", "convolution"}
	}
	return nil
}

func (s *studioApp) addInsert(v workspace, csrf string, c mixerChannel) gosx.Node {
	kinds := insertKinds(c)
	if c.Kind == "bus" && c.ID == "music" && len(c.Strip.Inserts) > 0 {
		return gosx.El("p", gosx.Text("The music bus takes one compressor, and it has one."))
	}
	if (c.Kind == "track" && len(c.Strip.Inserts) >= 8) || (c.Kind == "master" && len(c.Strip.Inserts) >= 16) {
		return gosx.El("p", gosx.Text("This chain is full."))
	}
	if c.Kind == "bus" && c.ID == "sfx" {
		return gosx.El("p", gosx.Text("This bus takes no inserts"))
	}
	if len(kinds) == 0 {
		return gosx.Fragment()
	}
	fields := append([]gosx.Node{hidden("source_revision", v.SourceRevision), hidden("inserton", c.ID)}, effectFields(kinds)...)
	if c.Kind == "master" {
		fields = append(fields, field("Impulse asset (for convolution)", textInput("asset", "")))
	}
	if v.Edition == 1 {
		fields = append(fields, upgradeConfirm())
	}
	fields = append(fields, submit("", "", "Add insert"))
	return s.form(v, csrf, "mixer", "add-effect", fields...)
}

func (s *studioApp) addReturn(v workspace, csrf string, mixer mixerState) gosx.Node {
	delay, reverbs := false, 0
	for _, e := range mergeMixerReturns(mixer.Returns, mixer.Effects) {
		if e.Kind == "delay" || e.Kind == "tape" {
			delay = true
		}
		if e.Kind == "reverb" {
			reverbs++
		}
	}
	var kinds []string
	var notes []gosx.Node
	if !delay {
		kinds = append(kinds, "delay", "tape")
	} else {
		notes = append(notes, gosx.El("p", gosx.Text("Delay or tape return is full.")))
	}
	if reverbs < 7 {
		kinds = append(kinds, "reverb")
	} else {
		notes = append(notes, gosx.El("p", gosx.Text("Reverb returns are full (7).")))
	}
	if len(kinds) > 0 {
		fields := append([]gosx.Node{hidden("source_revision", v.SourceRevision)}, effectFields(kinds)...)
		if v.Edition == 1 {
			fields = append(fields, upgradeConfirm())
		}
		fields = append(fields, submit("", "", "Add return"))
		notes = append(notes, s.form(v, csrf, "mixer", "add-effect", fields...))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-add-return")), gosx.El("strong", gosx.Text("Send return")), gosx.Fragment(notes...))
}
