package main

import (
	"context"
	"errors"
	"net/http"
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
	reply := func(kept bool) error {
		ctx.FormData = nil
		message := "Recovery copy kept. The score file is unchanged."
		if !kept {
			message = "No unsaved Code edits. The score file has this text."
		}
		return ctx.Success(message, map[string]any{"id": id, "sequence": sequence, "kept": kept})
	}
	if f["intent"] == "clear" {
		err = s.notes.closeSource(owner, id, f["file"], sequence, f["recoveryParentId"], f["recoveryParentHash"])
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
	// file's text when it is compared.
	copy, err := s.notes.saveSource(owner, savedSource{ID: id, Sequence: sequence, File: f["file"], Revision: f["fileRevision"], Source: f["content"], ParentID: f["recoveryParentId"], ParentHash: f["recoveryParentHash"]})
	if errors.Is(err, errSourceCheckpoint) {
		// A late or repeated checkpoint of a copy that is closed: if the file
		// has its text, nothing is lost and nothing needs keeping.
		if text, ok := s.fileText(ctx.Request.Context(), f["file"]); ok && sameSourceText(f["content"], text) {
			return reply(false)
		}
		return action.Error(409, err.Error())
	}
	if err != nil {
		return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
	}
	if text, ok := s.fileText(ctx.Request.Context(), f["file"]); ok && sameSourceText(copy.Source, text) {
		if closed, err := s.notes.closeSourceCopy(owner, copy); closed && err == nil {
			return reply(false)
		}
	}
	return reply(true)
}

// fileText reads a project file's current text from the service, the text
// the Code panel shows for it; file "" is the score when the project lists no
// files. ok is false when the text cannot be read within two seconds.
func (s *studioApp) fileText(ctx context.Context, file string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if file == "" {
		var score struct {
			Source string `json:"source"`
		}
		if s.backend.call(ctx, http.MethodGet, "/api/workspace", nil, &score) != nil {
			return "", false
		}
		return score.Source, true
	}
	var listing struct {
		Files []projectFile `json:"files"`
	}
	if s.backend.call(ctx, http.MethodGet, "/api/files", nil, &listing) != nil {
		return "", false
	}
	for _, candidate := range listing.Files {
		if candidate.Name == file {
			return candidate.Source, true
		}
	}
	return "", false
}
