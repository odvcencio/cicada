package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"m31labs.dev/gosx/action"
)

func (s *studioApp) checkpointSource(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	if s.notes == nil {
		return action.Error(503, "Code recovery is unavailable. Keep this window open.")
	}
	f := ctx.FormData
	id := f["recoveryId"]
	sequence, err := strconv.ParseUint(f["recoverySequence"], 10, 64)
	if err != nil || sequence == 0 || !validNoteID(id) || !validSourceFile(f["file"]) {
		return action.Validation("Invalid Code recovery receipt.", nil, nil)
	}
	owner := workspaceOwner(ctx.Request)
	if f["intent"] == "clear" {
		// The editor holds the file's text again; when it is edited, the
		// browser starts a new copy, so this one is closed for good.
		err = s.notes.closeSource(owner, id, f["file"], sequence, true, f["recoveryParentId"], f["recoveryParentHash"])
		if errors.Is(err, errSourceCheckpoint) {
			return action.Error(409, err.Error())
		}
		if err != nil {
			return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
		}
		ctx.FormData = nil
		return ctx.Success("Recovery copy kept. The score file is unchanged.", map[string]any{"id": id, "sequence": sequence})
	}
	// The copy is kept first and compared with the file second, so recovery
	// never waits for the service, and a copy is closed only if it holds the
	// file's text when it is compared. A refused checkpoint reads nothing.
	copy, err := s.notes.saveSource(owner, savedSource{ID: id, Sequence: sequence, File: f["file"], Revision: f["fileRevision"], Source: f["content"], ParentID: f["recoveryParentId"], ParentHash: f["recoveryParentHash"]})
	if errors.Is(err, errSourceCheckpoint) {
		return action.Error(409, err.Error())
	}
	if err != nil {
		return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
	}
	ctx.FormData = nil
	if revision, ok := s.fileRevision(ctx.Request.Context(), copy.File); ok && sameSourceRevision(copy.Source, revision) {
		if closed, err := s.notes.closeSourceCopy(owner, copy); closed && err == nil {
			// The file can move between the read and the close: read again.
			if again, ok := s.fileRevision(ctx.Request.Context(), copy.File); ok && again == revision {
				// revision lets the editor take the file's text as its own,
				// so it follows the file like any editor without changes.
				return ctx.Success("No unsaved Code edits. The score file has this text.", map[string]any{"id": id, "sequence": sequence, "kept": false, "revision": revision})
			}
			if err := s.notes.reopenSourceCopy(owner, copy); err != nil {
				return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
			}
		}
	}
	return ctx.Success("Recovery copy kept. The score file is unchanged.", map[string]any{"id": id, "sequence": sequence, "kept": true})
}

// fileRevision reads the revision (edits.Revision) of a project file's text as
// it is on disk; file "" is the score. The service reads only that file and
// takes no lock, so musical edits never wait for it. ok is false when it
// cannot be read within two seconds.
func (s *studioApp) fileRevision(ctx context.Context, file string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var answer struct {
		Revision string `json:"revision"`
	}
	if s.backend.call(ctx, http.MethodGet, "/api/files/revision?file="+url.QueryEscape(file), nil, &answer) != nil || answer.Revision == "" {
		return "", false
	}
	return answer.Revision, true
}
