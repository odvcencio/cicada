package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/session"
)

type draft struct {
	ID                                    string
	Sequence                              uint64
	ParentID, ParentHash                  string
	Source, Revision, Owner               string
	CheckRevision, CheckError             string
	PlayedCheckRevision, PlayedCheckError string
	File                                  string
	Kind                                  string
	Expires                               time.Time
	SavedNoteID                           string
	RecoveryError                         string
	Updated                               time.Time
}

// CSRF tokens are masked per response. Private drafts and live mount leases
// use a separate, stable identity stored in the encrypted workspace session.
func workspaceOwner(r *http.Request) string {
	return session.Current(r).String("workspace-owner")
}

// Cookie sessions hold a small draft receipt, never an entire source document.
// A rejected submission can contain up to 2 MiB. Retain its receipt across
// navigation and reload; managed submissions also keep the existing editor.
func (s *studioApp) preserveDraft(ctx *action.Context) map[string]string {
	// HTML form submission encodes textarea newlines as CRLF; checkpoints
	// use the browser's LF value. They represent the same editor buffer.
	source := strings.ReplaceAll(ctx.FormData["content"], "\r\n", "\n")
	revision := ctx.FormData["fileRevision"]
	if revision == "" {
		revision = ctx.FormData["revision"]
	}
	var key string
	var err error
	if s.notes != nil && ctx.FormData["recoveryId"] != "" {
		sequence, parseErr := strconv.ParseUint(ctx.FormData["recoverySequence"], 10, 64)
		if parseErr != nil {
			sequence = 0 // Preserve the submission in RAM if its receipt is invalid.
		}
		key = ctx.FormData["recoveryId"]
		_, err = s.notes.saveSource(workspaceOwner(ctx.Request), savedSource{ID: key, Sequence: sequence, File: ctx.FormData["file"], Revision: revision, Source: source, ParentID: ctx.FormData["recoveryParentId"], ParentHash: ctx.FormData["recoveryParentHash"]})
	} else {
		key, err = s.storeFileDraft(source, revision, workspaceOwner(ctx.Request), "source", ctx.FormData["file"])
	}
	if err != nil {
		// Disk failure must not turn a rejected save into a lost browser
		// submission. Keep a volatile receipt and clearly warn on projection.
		if key == "" {
			key = newNoteID()
		}
		if key == "" {
			return nil
		}
		s.draftMu.Lock()
		if s.drafts == nil {
			s.drafts = make(map[string]draft)
		}
		s.drafts[key] = draft{ID: key, Source: source, Revision: revision, Owner: workspaceOwner(ctx.Request), File: ctx.FormData["file"], Kind: "source", RecoveryError: err.Error(), Updated: time.Now(), Expires: time.Now().Add(30 * time.Minute)}
		s.draftMu.Unlock()
	}
	session.Current(ctx.Request).Set("score-draft", key)
	if action.WantsJSON(ctx.Request) {
		return ctx.FormData
	}
	return map[string]string{"draft": key}
}

func (s *studioApp) storeDraft(source, revision, owner, kind string) (string, error) {
	return s.storeFileDraft(source, revision, owner, kind, "")
}

func (s *studioApp) storeFileDraft(source, revision, owner, kind, file string) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	key := hex.EncodeToString(id[:])
	if kind == "source" && s.notes != nil {
		_, err := s.notes.saveSource(owner, savedSource{ID: key, Sequence: 1, File: file, Revision: revision, Source: source})
		return key, err
	}
	now := time.Now()
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	if s.drafts == nil {
		s.drafts = make(map[string]draft)
	}
	for id, d := range s.drafts {
		if d.Owner == owner && d.Kind == kind && d.File == file || now.After(d.Expires) {
			delete(s.drafts, id)
		}
	}
	// At most sixteen drafts, all covered by the request-body limit.
	if len(s.drafts) >= 16 {
		var oldest string
		var at time.Time
		for id, d := range s.drafts {
			if oldest == "" || d.Expires.Before(at) {
				oldest, at = id, d.Expires
			}
		}
		delete(s.drafts, oldest)
	}
	s.drafts[key] = draft{Source: source, Revision: revision, Owner: owner, Kind: kind, File: file, Expires: now.Add(30 * time.Minute)}
	return key, nil
}

// Each project file has its own recovery slot. A check or rejected save in a
// second file must not replace the draft awaiting correction in the first.
func (s *studioApp) sourceDraft(r *http.Request, file string) (draft, []savedSource, error) {
	owner := workspaceOwner(r)
	if owner == "" {
		return draft{}, nil, nil
	}
	if s.notes != nil {
		copies, err := s.notes.sourceCopies(owner, file)
		selected := r.URL.Query().Get("codeDraft")
		s.draftMu.Lock()
		var volatile draft
		for _, candidate := range s.drafts {
			if candidate.Owner == owner && candidate.File == file && candidate.Kind == "source" && candidate.RecoveryError != "" && (selected == "" || selected == candidate.ID) && time.Now().Before(candidate.Expires) && candidate.Updated.After(volatile.Updated) {
				volatile = candidate
			}
		}
		s.draftMu.Unlock()
		if volatile.Kind != "" && (len(copies) == 0 || volatile.Updated.After(copies[0].Updated) || selected == volatile.ID) {
			return volatile, copies, err
		}
		if len(copies) == 0 {
			return draft{}, copies, err
		}
		for _, copy := range copies {
			if selected == "" || copy.ID == selected {
				return sourceDraft(copy, owner), copies, err
			}
		}
		return draft{}, copies, err
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	for _, d := range s.drafts {
		if d.Owner == owner && d.Kind == "source" && d.File == file && time.Now().Before(d.Expires) {
			return d, nil, nil
		}
	}
	return draft{}, nil, nil
}

// Keep the legacy receipt pointed at another retained source draft. Other
// workflows use its presence to avoid replacing a score with unsaved work.
func (s *studioApp) clearSourceDraft(r *http.Request, file string, fields map[string]string) error {
	owner := workspaceOwner(r)
	if s.notes != nil {
		id := fields["recoveryId"]
		if id != "" {
			if err := s.notes.closeSource(owner, id, file, fields["recoveryParentId"], fields["recoveryParentHash"]); err != nil {
				return err
			}
		}
	}
	now := time.Now()
	s.draftMu.Lock()
	var receipt string
	var expires time.Time
	for id, d := range s.drafts {
		selected := s.notes == nil || id == fields["recoveryId"] || id == fields["recoveryParentId"] && sourceFingerprint(d.Source) == fields["recoveryParentHash"]
		if now.After(d.Expires) || selected && d.Owner == owner && d.Kind == "source" && d.File == file {
			delete(s.drafts, id)
			continue
		}
		if d.Owner == owner && d.Kind == "source" && (receipt == "" || d.Expires.After(expires)) {
			receipt, expires = id, d.Expires
		}
	}
	s.draftMu.Unlock()
	store := session.Current(r)
	if receipt == "" {
		store.Delete("score-draft")
	} else {
		store.Set("score-draft", receipt)
	}
	return nil
}

func (s *studioApp) draft(id, owner string) (draft, bool) {
	s.draftMu.Lock()
	d, ok := s.drafts[id]
	s.draftMu.Unlock()
	if ok && d.Owner == owner && !time.Now().After(d.Expires) {
		return d, true
	}
	if s.notes != nil {
		if saved, err := s.notes.getSource(owner, id); err == nil && saved.State == "ready" {
			return sourceDraft(saved, owner), true
		}
		if saved, err := s.notes.get(owner, id); err == nil && saved.State == "ready" {
			s.cacheNoteDraft(saved, owner)
			return noteDraft(saved, owner), true
		}
	}
	return draft{}, false
}

func sourceDraft(saved savedSource, owner string) draft {
	return draft{ID: saved.ID, Sequence: saved.Sequence, ParentID: saved.ParentID, ParentHash: saved.ParentHash, Source: saved.Source, Revision: saved.Revision, Owner: owner, File: saved.File, Kind: "source"}
}
