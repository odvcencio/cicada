package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/gosx/action"
)

// renameTrack uses the same project-wide, revision-checked intent as agents.
// The source receipt includes secondary files, so an unseen save is refused.
func (s *studioApp) renameTrack(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	if f["revision"] == "" || f["source_revision"] == "" {
		return action.Error(http.StatusConflict, "Refresh Studio before renaming a track.")
	}
	if strings.TrimSpace(f["track"]) == "" || strings.TrimSpace(f["name"]) == "" {
		return action.Validation("Choose a track and enter a new name.", nil, f)
	}
	if err := s.guardRetainedNoteWork(ctx); err != nil {
		return err
	}
	env := edit.Envelope{Version: edit.EnvelopeVersion, Revision: f["revision"], Intents: []edit.Intent{&edit.Rename{Entity: edit.EntityID("track:" + f["track"]), Name: f["name"]}}}
	var result any
	if err := s.backend.call(context.WithValue(ctx.Request.Context(), expectedProjectSourcesKey{}, f["source_revision"]), http.MethodPost, "/api/intents", env, &result); err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			refusal := action.Error(failure.Status, failure.Message)
			refusal.Result.Values = f
			return refusal
		}
		unknown := action.Error(http.StatusServiceUnavailable, "Could not confirm whether the track was renamed. Refresh saved state before retrying.")
		unknown.Result.Values = f
		return unknown
	}
	ctx.FormData = nil
	ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/?panel=session"), "Track renamed.")
	return nil
}

// A retained review or recovery checkpoint can still refer to the old track
// after its Live engine releases its local copy. Keep it reviewable before an
// identity-changing edit; the shell uses the same conservative rule for history.
func (s *studioApp) hasRetainedNoteWork(ctx *action.Context) (bool, error) {
	if s.notes == nil {
		return false, nil
	}
	pending, err := s.notes.pending(workspaceOwner(ctx.Request))
	if err != nil {
		return false, action.Error(http.StatusServiceUnavailable, "Could not check retained note takes. Refresh before retrying.")
	}
	return len(pending) > 0, nil
}

func (s *studioApp) guardRetainedNoteWork(ctx *action.Context) error {
	pending, err := s.hasRetainedNoteWork(ctx)
	if err != nil {
		return err
	}
	if pending {
		return action.Error(http.StatusConflict, "Commit or discard retained note takes before renaming a track.")
	}
	return nil
}
