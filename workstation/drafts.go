package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/session"
)

type draft struct {
	Source, Revision, Owner string
	Kind                    string
	Expires                 time.Time
}

// Cookie sessions hold a small draft receipt, never an entire source document.
// A rejected submission can contain up to 2 MiB. Retain its receipt across
// navigation and reload; managed submissions also keep the existing editor.
func (s *studioApp) preserveDraft(ctx *action.Context) map[string]string {
	key, err := s.storeDraft(ctx.FormData["content"], ctx.FormData["revision"], session.Token(ctx.Request), "source")
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
		if d.Owner == owner && d.Kind == kind || now.After(d.Expires) {
			delete(s.drafts, id)
		}
	}
	// At most sixteen score drafts, all covered by the request-body limit.
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
	s.drafts[key] = draft{Source: source, Revision: revision, Owner: owner, Kind: kind, Expires: now.Add(30 * time.Minute)}
	return key, nil
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
