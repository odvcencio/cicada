package main

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/migration"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// studioCompile is the compile step shared by the edit service and the commit
// tail; tests wrap it to count compiles.
var studioCompile = compileStudioSourceWithOverrides

// studioCompiler compiles a candidate for the edit service with the same
// checks Studio applies before it writes a score. It keeps its latest
// successful compile so the commit tail can reuse it instead of compiling the
// same bytes again.
type studioCompiler struct {
	path    string
	source  []byte
	files   map[string][]byte
	project *project.Project
}

func (c *studioCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	overrides := make(map[string][]byte, len(files)+1)
	for path, data := range files {
		overrides[path] = data
	}
	p, err := studioCompile(c.path, source, overrides)
	if err != nil {
		return nil, err
	}
	c.source, c.files, c.project = bytes.Clone(source), make(map[string][]byte, len(files)), p
	for path, data := range files {
		c.files[path] = bytes.Clone(data)
	}
	return project.EditPlan(p, source), nil
}

// compiled returns the project from the latest compile when it was made from
// exactly this source and these auxiliary files, and nil otherwise.
func (c *studioCompiler) compiled(source []byte, files []edits.File) *project.Project {
	if c.project == nil || !bytes.Equal(c.source, source) || len(c.files) != len(files) {
		return nil
	}
	for _, file := range files {
		if data, ok := c.files[file.Path]; !ok || !bytes.Equal(data, file.After) {
			return nil
		}
	}
	return c.project
}

// upgradeEdition is the Options.UpgradeEdition hook: the edition-1 to 2
// rewrite Studio's mixer route performs (migration.FixSource, notation.Format,
// then a header or a manifest change), returning the manifest change as a file.
func (s *studio) upgradeEdition(manifestPath string) func([]byte) ([]byte, []edits.File, error) {
	return func(source []byte) ([]byte, []edits.File, error) {
		fixed, _, err := migration.FixSource(bytes.Clone(source))
		if err != nil {
			return nil, nil, err
		}
		document, err := notation.ParseDocument(fixed)
		if err != nil {
			return nil, nil, err
		}
		working, err := notation.Format(document)
		if err != nil {
			return nil, nil, err
		}
		if manifestPath == "" {
			if !bytes.HasPrefix(working, []byte("cicada 2\n")) {
				working = append([]byte("cicada 2\n\n"), working...)
			}
			return working, nil, nil
		}
		before, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, nil, err
		}
		after, changed, err := edition.UpgradeManifestEdition(before)
		if err != nil {
			return nil, nil, err
		}
		var files []edits.File
		if changed {
			files = append(files, edits.File{Path: manifestPath, Before: before, After: after})
		}
		return working, files, nil
	}
}

func (s *studio) editOptions() (edits.Options, error) {
	editionNumber, manifestPath, err := scoreEdition(s.path)
	if err != nil {
		return edits.Options{}, err
	}
	opts := edits.Options{Compiler: &studioCompiler{path: s.path}, RenderCheck: s.renderCheck, Path: s.path, Edition: editionNumber, ManifestPath: manifestPath, Now: time.Now, UpgradeEdition: s.upgradeEdition(manifestPath)}
	if manifestPath != "" {
		if opts.Manifest, err = os.ReadFile(manifestPath); err != nil {
			return edits.Options{}, err
		}
	}
	opts.ParseProject = func(source []byte) (*notation.Score, []notation.Diagnostic, error) {
		return parseScoreForPath(s.path, source)
	}
	return opts, nil
}

// auxiliaryFiles turns edit-service files into Studio's commit files; new
// files get mode 0o644.
func auxiliaryFiles(files []edits.File) ([]studioAuxiliaryFile, error) {
	result := make([]studioAuxiliaryFile, 0, len(files))
	for _, file := range files {
		mode := os.FileMode(0o644)
		info, err := os.Stat(file.Path)
		if err == nil {
			mode = info.Mode().Perm()
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		result = append(result, studioAuxiliaryFile{Path: file.Path, Before: file.Before, After: file.After, Mode: mode})
	}
	return result, nil
}

// applyIntents is the intent-side twin of applyWithResult: the same prologue,
// then edits.Apply, then the shared commit tail.
func (s *studio) applyIntents(w http.ResponseWriter, edit studioEdit, env edits.Envelope, writer edits.ParamWriter, beforeSwap func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := studioRecoveryConflict(s.path); err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	current, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if s.history != nil {
		bar, step := s.historyPosition()
		s.history.observe(current, bar, step)
	}
	if studioRevision(current) != edit.Revision {
		// The body also carries the canonical state for clients that queue edits.
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed on disk; reload before saving", "revision": studioRevision(current), "source": string(current)})
		return
	}
	opts, err := s.editOptions()
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	opts.ParamWriter = writer
	env.Version, env.Revision = edits.EnvelopeVersion, edit.Revision
	if env.Author == "" {
		env.Author = edit.Author
	}
	if env.Session == "" {
		env.Session = edit.Session
	}
	compiler := opts.Compiler.(*studioCompiler)
	result, err := edits.Apply(current, env, opts)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	if edit.Label == "" && result.Label != "" {
		edit.Label = result.Label
	}
	files, err := auxiliaryFiles(result.Files)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	mutation := studioMutation{Source: result.Source, Response: result.Response, Files: files}
	outcome := s.commitMutationLocked(edit, current, mutation, beforeSwap, studioHistoryWriteNew, 0, commitHook{envelope: &env, compiled: compiler.compiled(result.Source, result.Files)})
	studioJSON(w, outcome.Status, outcome.Response)
}
