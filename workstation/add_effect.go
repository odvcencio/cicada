package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"m31labs.dev/cicada/edit"
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
	if !edit.ValidMixerIdentifier(f["name"]) {
		return action.Validation("Enter a valid effect name using lowercase letters, digits, underscores or hyphens (at most 64 characters).", nil, f)
	}
	kinds := " drive eq comp deesser ampcab chorus transient width limiter convolution delay tape reverb "
	if !strings.Contains(kinds, " "+f["effectkind"]+" ") || f["effectkind"] == "" {
		return action.Validation("Choose an effect kind.", nil, f)
	}
	in := &edit.AddEffect{Name: f["name"], EffectKind: f["effectkind"], InsertOn: f["inserton"], ConfirmUpgrade: f["confirmUpgrade"] == "on"}
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
			refusal.Result.Data = failure.Data
			return refusal
		}
		return action.Error(http.StatusServiceUnavailable, "Could not confirm whether the effect was added. Refresh saved state before retrying.")
	}
	ctx.FormData = nil
	ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/?panel=mixer"), "Effect added.")
	return nil
}

func freeEffectName(v workspace, kind string) string {
	taken := map[string]bool{"music": true, "sfx": true, "master": true}
	if v.Project != nil {
		for _, e := range v.Project.Effects {
			taken[e.ID] = true
		}
		for _, t := range v.Project.Tracks {
			taken[t.ID] = true
		}
		for _, b := range v.Project.Buses {
			taken[b.ID] = true
		}
	}
	name := kind
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s%d", kind, i)
	}
	return name
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
	if c.Kind == "bus" && c.ID == "sfx" {
		return gosx.El("p", gosx.Text("This bus takes no inserts"))
	}
	if len(kinds) == 0 {
		return gosx.Fragment()
	}
	fields := []gosx.Node{hidden("source_revision", v.SourceRevision), hidden("inserton", c.ID), field("Kind", selectInput("effectkind", kinds[0], kinds)), field("Name", textInput("name", freeEffectName(v, kinds[0])))}
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
		fields := []gosx.Node{hidden("source_revision", v.SourceRevision), field("Kind", selectInput("effectkind", kinds[0], kinds)), field("Name", textInput("name", freeEffectName(v, kinds[0])))}
		if v.Edition == 1 {
			fields = append(fields, upgradeConfirm())
		}
		fields = append(fields, submit("", "", "Add return"))
		notes = append(notes, s.form(v, csrf, "mixer", "add-effect", fields...))
	}
	return gosx.El("div", gosx.Fragment(notes...))
}
