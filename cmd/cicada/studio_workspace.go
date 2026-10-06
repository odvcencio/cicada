package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
)

// workspace is the atomic score snapshot consumed by the GoSX workstation.
// Mutations still pass through applyWithResult and its revision/file checks.
func (s *studio) workspace(w http.ResponseWriter, r *http.Request) {
	s.observeHistory()
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p, parseErr := compileStudioSource(s.path, source)
	if parseErr == nil {
		s.lastGoodSource, s.lastGoodProject = bytes.Clone(source), p
	}
	message := ""
	if parseErr != nil {
		message = parseErr.Error()
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, map[string]any{
		"source": string(source), "revision": studioRevision(source),
		"project": s.lastGoodProject, "valid": parseErr == nil,
		"error": message, "filename": filepath.Base(s.path),
	})
}
