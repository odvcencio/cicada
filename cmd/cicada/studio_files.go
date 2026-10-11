package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioProjectFile struct {
	Name        string                `json:"name"`
	Source      string                `json:"source"`
	Revision    string                `json:"revision"`
	Diagnostics []notation.Diagnostic `json:"diagnostics"`
}

func studioProjectRoot(path string) (string, error) {
	sources, err := project.ReadSources(path, nil)
	if err != nil {
		return "", err
	}
	return sources.Root, nil
}

// Only sources admitted by the project loader may be selected. os.Root in the
// loader checks symlinks even for an unsaved editor buffer.
func (s *studio) filePath(name string) (string, error) {
	sources, err := project.ReadSources(s.path, nil)
	if err != nil {
		return "", err
	}
	for _, file := range sources.Files {
		relative, err := filepath.Rel(sources.Root, file.Path)
		if err == nil && filepath.ToSlash(relative) == name {
			return file.Path, nil
		}
	}
	return "", fmt.Errorf("file is not a project source")
}

func (s *studio) projectFileList() ([]studioProjectFile, error) {
	sources, err := project.ReadSources(s.path, nil)
	if err != nil {
		return nil, err
	}
	score, ds := sources.Parse()
	if score != nil {
		_, extra := project.FromScore(score)
		ds = append(ds, extra...)
	}
	files := make([]studioProjectFile, 0, len(sources.Files))
	for _, file := range sources.Files {
		name, _ := filepath.Rel(sources.Root, file.Path)
		entry := studioProjectFile{Name: filepath.ToSlash(name), Source: string(file.Source), Revision: studioRevision(file.Source), Diagnostics: []notation.Diagnostic{}}
		for _, d := range ds {
			if d.Position.File == file.Path || d.Position.File == "" && file.Path == s.path {
				d.Position.File = entry.Name
				entry.Diagnostics = append(entry.Diagnostics, d)
			}
		}
		files = append(files, entry)
	}
	return files, nil
}

func (s *studio) projectFiles(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.projectFileList()
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	hash, err := playSourceHash(s.path)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	studioJSON(w, 200, map[string]any{"files": files, "projectRevision": fmt.Sprintf("%x", hash)})
}

func (s *studio) editFile(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	path, err := s.filePath(edit.File)
	if err != nil {
		studioJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if edit.Action == "check" {
		score, ds, err := project.LoadScore(s.path, map[string][]byte{path: []byte(edit.Source)})
		if err == nil && score != nil {
			_, extra := project.FromScore(score)
			ds = append(ds, extra...)
		}
		valid := err == nil && !hasDiagnosticErrors(ds)
		response := map[string]any{"valid": valid, "diagnostics": ds}
		if err != nil {
			response["error"] = err.Error()
		}
		studioJSON(w, 200, response)
		return
	}
	s.mu.Lock()
	target := s
	if path != s.path {
		if s.fileSessions == nil {
			s.fileSessions = map[string]*studio{}
		}
		target = s.fileSessions[path]
		if target == nil {
			source, readErr := os.ReadFile(path)
			if readErr != nil {
				s.mu.Unlock()
				studioJSON(w, 500, map[string]any{"error": readErr.Error()})
				return
			}
			target = &studio{path: path, sessionID: newStudioSessionID(), transport: s.transport, history: newStudioHistory(source)}
			s.fileSessions[path] = target
		}
	}
	s.mu.Unlock()
	if edit.Action == "undo" || edit.Action == "redo" {
		// Reuse the revision-checked history request, including late-writer guards.
		if edit.Action == "undo" {
			target.undo(w, studioEditRequest(r, edit))
		} else {
			target.redo(w, studioEditRequest(r, edit))
		}
		return
	}
	edit.Label = studioEditLabel(edit)
	target.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{&edits.ReplaceText{Source: edit.Source}}}, edits.ParamWriterAuto, nil)
}

func studioEditRequest(r *http.Request, edit studioEdit) *http.Request {
	data, _ := json.Marshal(edit)
	copy := r.Clone(r.Context())
	copy.Body = io.NopCloser(bytes.NewReader(data))
	return copy
}

func (s *studio) projectRevision(w http.ResponseWriter, r *http.Request) {
	hash, err := playSourceHash(s.path)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	studioJSON(w, 200, map[string]any{"projectRevision": fmt.Sprintf("%x", hash)})
}
