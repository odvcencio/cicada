package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/gosx/action"
)

func (s *studioApp) structureAction(kind string) action.Handler {
	return func(ctx *action.Context) error {
		if err := actionValues(ctx); err != nil {
			return err
		}
		f := ctx.FormData
		if f["revision"] == "" || f["source_revision"] == "" {
			return action.Error(http.StatusConflict, "Refresh Studio before editing the arrangement.")
		}
		var intent edit.Intent
		var message string
		switch kind {
		case "scene-rename":
			intent, message = &edit.Rename{Entity: edit.EntityID("scene:" + f["scene"]), Name: f["name"]}, "Scene renamed."
		case "scene-duplicate":
			intent, message = &edit.DuplicateScene{Scene: f["scene"], Name: f["name"]}, "Scene duplicated. Use Add section to place it in the song."
		case "scene-add":
			intent, message = &edit.AddScene{Name: f["name"]}, "Scene created. Use Add section to place it in the song."
		case "scene-delete":
			intent, message = &edit.DeleteScene{Scene: f["scene"]}, "Scene deleted."
		case "track-delete":
			intent, message = &edit.DeleteTrack{Track: f["track"]}, "Track deleted."
		case "song-convert":
			intent, message = &edit.ConvertSongToArrange{}, "Song converted to placements."
		}
		if (kind == "scene-rename" || kind == "scene-duplicate" || kind == "scene-add") && strings.TrimSpace(f["name"]) == "" || (kind == "scene-rename" || kind == "scene-duplicate" || kind == "scene-delete") && strings.TrimSpace(f["scene"]) == "" || kind == "track-delete" && strings.TrimSpace(f["track"]) == "" {
			return action.Validation("Choose an item and enter a name where needed.", nil, f)
		}
		if err := s.guardRetainedNoteWork(ctx); err != nil {
			return err
		}
		env := edit.Envelope{Version: edit.EnvelopeVersion, Revision: f["revision"], Intents: []edit.Intent{intent}}
		var result any
		if err := s.backend.call(context.WithValue(ctx.Request.Context(), expectedProjectSourcesKey{}, f["source_revision"]), http.MethodPost, "/api/intents", env, &result); err != nil {
			var failure *backendError
			if errors.As(err, &failure) {
				refusal := action.Error(failure.Status, failure.Message)
				refusal.Result.Values = f
				return refusal
			}
			unknown := action.Error(http.StatusServiceUnavailable, "Could not confirm the change. Refresh saved state before retrying.")
			unknown.Result.Values = f
			return unknown
		}
		ctx.FormData = nil
		ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/?panel=session"), message)
		return nil
	}
}
