package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"unicode/utf8"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// workspace is the atomic score snapshot consumed by the GoSX workstation.
// Mutations pass through the intent commit tail and its revision/file checks.
func (s *studio) workspace(w http.ResponseWriter, r *http.Request) {
	s.observeHistory()
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p, sources, diagnostics, parseErr := s.workspaceCompile(source)
	if parseErr == nil {
		s.lastGoodSource, s.lastGoodProject = bytes.Clone(source), p
	}
	message := ""
	if parseErr != nil {
		message = parseErr.Error()
	}
	var files []studioProjectFile
	if sources != nil {
		files = studioFileRows(sources, diagnostics, s.path)
	} else {
		files, _ = s.projectFileList()
	}
	projectHash, _ := playSourceHash(s.path)
	sourceRevision := ""
	if sources != nil {
		sourceRevision = sources.ScoreRevision()
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, map[string]any{
		"files": files, "projectRevision": fmt.Sprintf("%x", projectHash),
		"source": string(source), "revision": studioRevision(source),
		"source_revision": sourceRevision,
		"project":         s.lastGoodProject, "valid": parseErr == nil,
		"error": message, "filename": filepath.Base(s.path),
	})
}

// workspaceCompile is compileStudioSourceDetailed with file diagnostics,
// through the kept state when it still holds exactly the project on disk, so
// a workspace read and the next edit compile the score once between them.
// The caller holds s.mu.
func (s *studio) workspaceCompile(source []byte) (*project.Project, *project.Sources, []notation.Diagnostic, error) {
	if !utf8.Valid(source) {
		return compileStudioSourceDetailed(s.path, source, nil, true)
	}
	absolute, err := filepath.Abs(s.path)
	if err != nil {
		return compileStudioSourceDetailed(s.path, source, nil, true)
	}
	sources, err := project.ReadSources(s.path, map[string][]byte{absolute: source})
	if err != nil {
		return compileStudioSourceDetailed(s.path, source, nil, true)
	}
	state := s.keptState(source, sources)
	if state == nil {
		return compileStudioSourceDetailed(s.path, source, nil, true)
	}
	return state.Detailed(true)
}
