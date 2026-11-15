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
	// file's text when it is compared.
	copy := savedSource{ID: id, Sequence: sequence, File: f["file"], Revision: f["fileRevision"], Source: f["content"], ParentID: f["recoveryParentId"], ParentHash: f["recoveryParentHash"]}
	stored, err := s.notes.saveSource(owner, copy)
	if errors.Is(err, errSourceCheckpoint) {
		// A refused checkpoint reads nothing from the service, unless it
		// repeats one answered kept:false (that answer was lost): then the
		// same answer, if the file still holds its text.
		previous, readErr := s.notes.getSource(owner, id)
		if readErr != nil || previous.State != "closed" || previous.Matched == "" || previous.Sequence != sequence || previous.File != copy.File || previous.Source != copy.Source {
			return action.Error(409, err.Error())
		}
		copy.Updated = time.Now().UTC()
		return s.answerCopy(ctx, owner, copy, true)
	}
	if err != nil {
		return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
	}
	return s.answerCopy(ctx, owner, stored, false)
}

// answerCopy answers a checkpoint whose copy is stored (or, repeated, was
// closed for matching the file): a copy that holds the file's text is closed
// and answered kept:false with the file's revision, so the editor can take
// that text as its own; any other copy is kept.
func (s *studioApp) answerCopy(ctx *action.Context, owner string, copy savedSource, closed bool) error {
	ctx.FormData = nil
	notKept := func(revision string) error {
		return ctx.Success("No unsaved Code edits. The score file has this text.", map[string]any{"id": copy.ID, "sequence": copy.Sequence, "kept": false, "revision": revision})
	}
	kept := func() error {
		return ctx.Success("Recovery copy kept. The score file is unchanged.", map[string]any{"id": copy.ID, "sequence": copy.Sequence, "kept": true})
	}
	reopen := func() error {
		reopened, err := s.notes.reopenSourceCopy(owner, copy)
		if err != nil {
			return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
		}
		if !reopened {
			// Closed meanwhile by a Save or a Discard.
			return action.Error(409, errSourceCheckpoint.Error())
		}
		return kept()
	}
	file, ok := s.readFileRevision(ctx.Request.Context(), copy.File)
	if !ok || !file.holds(copy.Source) {
		if closed {
			return reopen()
		}
		return kept()
	}
	if closed {
		return notKept(file.revision)
	}
	if ok, err := s.notes.closeSourceCopy(owner, copy, file.revision); !ok || err != nil {
		return kept()
	}
	// The file can move between the read and the close: read again.
	if again, ok := s.readFileRevision(ctx.Request.Context(), copy.File); ok && again.holds(copy.Source) {
		return notKept(again.revision)
	}
	return reopen()
}

// readFileRevision reads the revisions of a project file's text as it is on
// disk; file "" is the score. The service reads only that file and takes no
// lock, so musical edits never wait for it. A service without that route
// (404, during an update) is asked for the file's text instead, which is
// slower but as exact. ok is false when neither answers within two seconds.
func (s *studioApp) readFileRevision(ctx context.Context, file string) (fileRevision, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var answer struct {
		Revision     string `json:"revision"`
		TextRevision string `json:"textRevision"`
	}
	err := s.backend.call(ctx, http.MethodGet, "/api/files/revision?file="+url.QueryEscape(file), nil, &answer)
	var failure *backendError
	if errors.As(err, &failure) && failure.Status == http.StatusNotFound {
		if text, ok := s.fileText(ctx, file); ok {
			return revisionOf(text), true
		}
		return fileRevision{}, false
	}
	if err != nil || answer.Revision == "" {
		return fileRevision{}, false
	}
	return fileRevision{answer.Revision, answer.TextRevision}, true
}

// fileText reads a project file's text the way the Code panel does, for a
// service without the revision route: the score, or a file of the project's
// list. It parses the project while it holds the service's lock.
func (s *studioApp) fileText(ctx context.Context, file string) (string, bool) {
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
