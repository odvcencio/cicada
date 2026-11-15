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
	"m31labs.dev/cicada/edit/rendercheck"
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
	return studioFileRows(sources, ds, s.path), nil
}

// studioFileRows builds Code file rows from captured sources and diagnostics.
func studioFileRows(sources *project.Sources, ds []notation.Diagnostic, entryPath string) []studioProjectFile {
	files := make([]studioProjectFile, 0, len(sources.Files))
	for _, file := range sources.Files {
		name, _ := filepath.Rel(sources.Root, file.Path)
		entry := studioProjectFile{Name: filepath.ToSlash(name), Source: string(file.Source), Revision: studioRevision(file.Source), Diagnostics: []notation.Diagnostic{}}
		for _, d := range ds {
			if d.Position.File == file.Path || d.Position.File == "" && file.Path == entryPath {
				d.Position.File = entry.Name
				entry.Diagnostics = append(entry.Diagnostics, d)
			}
		}
		files = append(files, entry)
	}
	return files
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
			target = &studio{path: path, sessionID: newStudioSessionID(), transport: s.transport, history: newStudioHistory(source), renderCheck: rendercheck.Cached(path, rendercheck.New(path))}
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

// fileRevision answers the revision of one project file's text as it is on
// disk, and textRevision, the revision of that text with each CRLF read as
// LF (a lone CR is kept); file "" is the score. Code recovery asks for it on
// every checkpoint, so it takes no lock and parses nothing: musical edits and
// transport commands never wait for it. A name that is not a project source,
// or a file that cannot be read, is answered 422; 404 means a service without
// this route.
func (s *studio) fileRevision(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	var source []byte
	var err error
	if name == "" {
		source, err = os.ReadFile(s.path)
	} else {
		known, ok := s.filePaths.Load(name)
		if !ok {
			found, findErr := s.fileLocation(name)
			if findErr != nil {
				studioJSON(w, 422, map[string]any{"error": findErr.Error()})
				return
			}
			known, _ = s.filePaths.LoadOrStore(name, found)
		}
		// Through os.Root: a symlink put in place after the name was checked
		// cannot lead outside the project.
		at := known.(studioFileLocation)
		var root *os.Root
		if root, err = os.OpenRoot(at.root); err == nil {
			source, err = root.ReadFile(at.name)
			root.Close()
		}
	}
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, 200, map[string]any{"file": name, "revision": studioRevision(source), "textRevision": studioRevision(bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n")))})
}

// studioFileLocation is a project file found once by name: the project's
// root and the file's path inside it.
type studioFileLocation struct{ root, name string }

// fileLocation is filePath for fileRevision: the file must be a project
// source inside the project's root.
func (s *studio) fileLocation(name string) (studioFileLocation, error) {
	sources, err := project.ReadSources(s.path, nil)
	if err != nil {
		return studioFileLocation{}, err
	}
	for _, file := range sources.Files {
		relative, err := filepath.Rel(sources.Root, file.Path)
		if err == nil && filepath.IsLocal(relative) && filepath.ToSlash(relative) == name {
			return studioFileLocation{root: sources.Root, name: relative}, nil
		}
	}
	return studioFileLocation{}, fmt.Errorf("file is not a project source")
}

func (s *studio) projectRevision(w http.ResponseWriter, r *http.Request) {
	hash, err := playSourceHash(s.path)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	studioJSON(w, 200, map[string]any{"projectRevision": fmt.Sprintf("%x", hash)})
}
