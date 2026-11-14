package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/gosx/action"
)

func (s *studioApp) splitPlacement(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	if f["revision"] == "" || f["source_revision"] == "" || f["target_key"] == "" {
		return action.Error(http.StatusConflict, "Refresh Studio before splitting this placement.")
	}
	if f["name"] == "" {
		return action.Validation("Choose a placement.", nil, f)
	}
	at, err := strconv.ParseInt(f["at_tick"], 10, 64)
	if err != nil || at < 0 {
		return action.Validation("Enter a nonnegative whole tick position.", nil, f)
	}
	if err := s.validateSplitTarget(ctx, at); err != nil {
		return err
	}
	env := edit.Envelope{Version: edit.EnvelopeVersion, Revision: f["revision"], Intents: []edit.Intent{&edit.SplitClip{Entity: edit.EntityID("placement:" + f["name"]), AtTick: at}}}
	request := context.WithValue(ctx.Request.Context(), expectedProjectSourcesKey{}, f["source_revision"])
	request = context.WithValue(request, splitIntentTimeoutKey{}, true)
	var result any
	if err := s.backend.call(request, http.MethodPost, "/api/intents", env, &result); err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			refusal := action.Error(failure.Status, failure.Message)
			refusal.Result.Values = f
			return refusal
		}
		unknown := action.Error(http.StatusServiceUnavailable, "Could not confirm whether the placement was split. Refresh saved state before retrying.")
		unknown.Result.Values = f
		return unknown
	}
	ctx.FormData = nil
	ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, placementURL(f["name"])), "Placement split.")
	return nil
}

// Recheck both the shown project and target before sending the intent. The
// backend verifies the same source receipt again at publication.
func (s *studioApp) validateSplitTarget(ctx *action.Context, at int64) error {
	f := ctx.FormData
	var saved workspace
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/workspace", nil, &saved); err != nil {
		return action.Error(http.StatusServiceUnavailable, "Cannot check the placement. Refresh Studio before retrying.")
	}
	if saved.Revision != f["revision"] || saved.SourceRevision != f["source_revision"] || saved.Project == nil || saved.Project.Arrange == nil {
		return action.Error(http.StatusConflict, "The project changed. Refresh Studio before splitting this placement.")
	}
	p := saved.Project
	for _, place := range p.Arrange.Placements {
		if place.ID != f["name"] {
			continue
		}
		if placementTargetKey(place) != f["target_key"] {
			return action.Error(http.StatusConflict, "This placement changed. Refresh Studio before splitting it.")
		}
		stepTrack := false
		for _, track := range p.Tracks {
			if track.ID == place.Track && track.Kind != "audio" {
				stepTrack = true
				break
			}
		}
		for _, pattern := range p.Patterns {
			if !stepTrack || pattern.ID != place.Content || pattern.Performance != nil {
				continue
			}
			step := int64(pattern.StepTicks)
			if step == 0 {
				step = 240
			}
			length := int64(pattern.Steps) * step
			if length <= 0 || at <= place.AtTick || at >= place.AtTick+place.LengthTicks || (at-place.AtTick)%length != 0 {
				return action.Validation("Choose a pattern-length boundary strictly inside the placement.", nil, f)
			}
			return nil
		}
		return action.Validation("Only step-pattern placements can be split here.", nil, f)
	}
	return action.Error(http.StatusConflict, "This placement is no longer in the arrangement. Refresh Studio.")
}
