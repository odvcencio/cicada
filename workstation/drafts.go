package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/session"
)

type draft struct {
	Source, Revision, Owner string
	File                    string
	Kind                    string
	Expires                 time.Time
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
	revision := ctx.FormData["fileRevision"]
	if revision == "" {
		revision = ctx.FormData["revision"]
	}
	key, err := s.storeFileDraft(ctx.FormData["content"], revision, workspaceOwner(ctx.Request), "source", ctx.FormData["file"])
	if err != nil {
		return nil
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
func (s *studioApp) sourceDraft(r *http.Request, file string) (draft, bool) {
	owner := workspaceOwner(r)
	if owner == "" {
		return draft{}, false
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	for _, d := range s.drafts {
		if d.Owner == owner && d.Kind == "source" && d.File == file && time.Now().Before(d.Expires) {
			return d, true
		}
	}
	return draft{}, false
}

// Keep the legacy receipt pointed at another retained source draft. Other
// workflows use its presence to avoid replacing a score with unsaved work.
func (s *studioApp) clearSourceDraft(r *http.Request, file string) {
	owner := workspaceOwner(r)
	now := time.Now()
	s.draftMu.Lock()
	var receipt string
	var expires time.Time
	for id, d := range s.drafts {
		if now.After(d.Expires) || d.Owner == owner && d.Kind == "source" && d.File == file {
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
}

func (s *studioApp) draft(id, owner string) (draft, bool) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	d, ok := s.drafts[id]
	if !ok || d.Owner != owner || time.Now().After(d.Expires) {
		return draft{}, false
	}
	return d, true
}
