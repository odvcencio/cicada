package main

import (
	"crypto/subtle"
	"net/http"

	"m31labs.dev/gosx/session"
)

// Only the private startup URL printed to the desktop parent carries this
// one-use capability. It never appears in workspace HTML or collaboration links.
func (s *studioApp) bootstrapLocalOwner(w http.ResponseWriter, r *http.Request) {
	s.bootstrapOwner(w, r, "/")
}

func (s *studioApp) bootstrapNativeOwner(w http.ResponseWriter, r *http.Request) {
	s.bootstrapOwner(w, r, "/studio")
}

func (s *studioApp) bootstrapOwner(w http.ResponseWriter, r *http.Request, destination string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()
	if s.bootstrapToken == "" || subtle.ConstantTimeCompare([]byte(r.PathValue("token")), []byte(s.bootstrapToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	store := session.Current(r)
	identity, err := s.notes.localOwner(store.String("collaboration-user"), workspaceOwner(r))
	if err != nil {
		http.Error(w, "The local workspace identity could not be restored. Saved performances are preserved.", 503)
		return
	}
	if store.String("collaboration-user") != identity.Actor || workspaceOwner(r) != identity.NotesOwner {
		store.Renew()
		store.Set("workspace-owner", identity.NotesOwner)
		store.Set("collaboration-user", identity.Actor)
		store.Set("workspace", true)
	}
	s.bootstrapToken = ""
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
