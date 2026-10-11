package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	// validationFiles supplies host-prepared pins during candidate validation.
	// They are still committed only when finish adds them to the result.
	validationFiles map[string][]byte
	path            string
	source          []byte
	files           map[string][]byte
	project         *project.Project
}

func (c *studioCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	overrides := make(map[string][]byte, len(files)+len(c.validationFiles))
	for path, data := range c.validationFiles {
		overrides[path] = data
	}
	for path, data := range files {
		overrides[path] = data
	}
	// The host compiler adds the entry source to overrides. Keep only the
	// auxiliary inputs when matching the compiled result to a commit.
	compiledFiles := make(map[string][]byte, len(overrides))
	for path, data := range overrides {
		compiledFiles[path] = bytes.Clone(data)
	}
	p, err := studioCompile(c.path, source, overrides)
	if err != nil {
		return nil, err
	}
	// The runtime project omits templates such as unused presets. Keep the
	// authored names from every source, including libraries, for allocation.
	sources, err := project.ReadSources(c.path, overrides)
	if err != nil {
		return nil, err
	}
	c.source, c.files, c.project = bytes.Clone(source), compiledFiles, p
	return project.EditPlan(p, source, sources.Files...), nil
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
func (s *studio) upgradeEdition(manifestPath string) func([]byte, map[string][]byte) ([]byte, []edits.File, error) {
	return func(source []byte, staged map[string][]byte) ([]byte, []edits.File, error) {
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
		before, ok := staged[manifestPath]
		if !ok {
			before, err = os.ReadFile(manifestPath)
			if err != nil {
				return nil, nil, err
			}
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

// editOptions supplies the complete project inputs for every intent route.
// source is the current entry buffer; files holds acceptance-time candidates.
func (s *studio) editOptions(source []byte, files map[string][]byte) (edits.Options, error) {
	editionNumber, manifestPath, err := scoreEdition(s.path)
	if err != nil {
		return edits.Options{}, err
	}
	// ScoreEdition's loose-file default is a fallback, not an inherited
	// manifest edition. Let Parse resolve an explicit header in a loose file.
	if manifestPath == "" {
		editionNumber = 0
	}
	absolute, err := filepath.Abs(s.path)
	if err != nil {
		return edits.Options{}, err
	}
	overrides := make(map[string][]byte, len(files)+1)
	for path, data := range files {
		overrides[path] = data
	}
	overrides[absolute] = source
	sources, err := project.ReadSources(s.path, overrides)
	if err != nil {
		return edits.Options{}, err
	}
	opts := edits.Options{Compiler: &studioCompiler{path: s.path}, RenderCheck: s.renderCheck, Path: s.path, Edition: editionNumber, ManifestPath: manifestPath, Sources: sources.Files, Now: time.Now, UpgradeEdition: s.upgradeEdition(manifestPath)}
	if manifestPath != "" {
		var supplied bool
		opts.Manifest, supplied = overrides[manifestPath]
		if !supplied {
			opts.Manifest, err = os.ReadFile(manifestPath)
		}
		if err != nil {
			return edits.Options{}, err
		}
		manifest, err := edition.ParseProjectManifest(opts.Manifest)
		if err != nil {
			return edits.Options{}, err
		}
		opts.Edition = manifest.Edition
	}
	opts.ParseProject = func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
		absolute, err := filepath.Abs(s.path)
		if err != nil {
			return nil, nil, err
		}
		if files == nil {
			files = make(map[string][]byte)
		}
		files[absolute] = source
		return project.LoadScore(s.path, files)
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
	s.applyPreparedIntents(w, edit, env, writer, nil, nil, beforeSwap)
}

// applyPreparedIntents keeps host IO around the pure text edit. prepare may
// resolve library items or supply auxiliary source bytes; finish may prepare
// pins and response data after Apply, before the shared commit tail.
func (s *studio) applyPreparedIntents(w http.ResponseWriter, edit studioEdit, env edits.Envelope, writer edits.ParamWriter, prepare func([]byte, *edits.Options) (edits.Envelope, error), finish func(*edits.Result) error, beforeSwap func()) {
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
	opts, err := s.editOptions(current, nil)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	opts.ParamWriter = writer
	if prepare != nil {
		env, err = prepare(current, &opts)
		if err != nil {
			studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
	}
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
	if finish != nil {
		if err := finish(result); err != nil {
			studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
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

type stagedProposal struct {
	proposal edits.Proposal
	staged   *edits.Staged
}

func (s *studio) stageProposal(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin edits are not allowed"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		studioJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "expected JSON"})
		return
	}
	var proposal edits.Proposal
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "expected one JSON request"})
		return
	}
	if proposal.Envelope.Revision == "" {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "missing revision"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.proposalSource(w, proposal.Envelope.Revision)
	if !ok {
		return
	}
	opts, err := s.editOptions(current, nil)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	staged, err := edits.Stage(current, proposal, opts)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	proposal.ID = hex.EncodeToString(raw[:])
	proposal.Label = staged.Result.Label
	if s.proposals == nil {
		s.proposals = make(map[string]stagedProposal)
	}
	s.proposals[proposal.ID] = stagedProposal{proposal: proposal, staged: staged}
	s.previewProposal = proposal.ID
	s.transport.applyPreview(staged.Ops)
	studioJSON(w, http.StatusOK, map[string]any{"id": proposal.ID, "diff": staged.Diff, "ops": staged.Ops})
}

// proposalSource checks the revision fixed at staging, never one supplied by
// an accept request. The caller holds s.mu; read-only staging adds no history.
func (s *studio) proposalSource(w http.ResponseWriter, revision string) ([]byte, bool) {
	if err := studioRecoveryConflict(s.path); err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return nil, false
	}
	current, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return nil, false
	}
	if studioRevision(current) != revision {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed on disk; reload before saving", "revision": studioRevision(current), "source": string(current)})
		return nil, false
	}
	return current, true
}

func (s *studio) acceptProposal(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin edits are not allowed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	staged, ok := s.proposals[id]
	if !ok {
		studioJSON(w, http.StatusNotFound, map[string]any{"error": "unknown proposal"})
		return
	}
	proposal, result := staged.proposal, staged.staged.Result
	current, ok := s.proposalSource(w, proposal.Envelope.Revision)
	if !ok {
		return
	}
	// Refuse changed auxiliary inputs before exchanging the entry source.
	for _, file := range result.Files {
		latest, err := os.ReadFile(file.Path)
		if errors.Is(err, os.ErrNotExist) && file.Before == nil {
			continue
		}
		if err != nil || !bytes.Equal(latest, file.Before) {
			studioJSON(w, http.StatusConflict, map[string]any{"error": "proposal file changed on disk; stage the proposal again", "revision": studioRevision(current), "source": string(current)})
			return
		}
	}
	files, err := auxiliaryFiles(result.Files)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	overrides := make(map[string][]byte, len(result.Files))
	for _, file := range result.Files {
		overrides[file.Path] = file.After
	}
	opts, err := s.editOptions(result.Source, overrides)
	if err == nil {
		_, err = opts.Compiler.Compile(result.Source, overrides)
	}
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	if s.history != nil {
		bar, step := s.historyPosition()
		s.history.observe(current, bar, step)
	}
	edit := studioEdit{Revision: proposal.Envelope.Revision, Label: proposal.Label, Author: proposal.Envelope.Author, Session: proposal.Envelope.Session}
	mutation := studioMutation{Source: result.Source, Files: files, Response: result.Response}
	// Revalidate at acceptance, including project dependencies that may have
	// changed since staging. The tail commits this candidate once.
	outcome := s.commitMutationLocked(edit, current, mutation, nil, studioHistoryWriteNew, 0, commitHook{envelope: &proposal.Envelope, compiled: opts.Compiler.(*studioCompiler).compiled(result.Source, result.Files)})
	if outcome.Status == http.StatusOK {
		s.removeProposal(id)
		if outcome.Written && s.previewProposal != "" {
			s.previewProposal = ""
			s.transport.applyPreview(nil)
		}
	} else if outcome.Status == http.StatusConflict {
		if latest, err := os.ReadFile(s.path); err == nil {
			outcome.Response["revision"], outcome.Response["source"] = studioRevision(latest), string(latest)
		}
	}
	studioJSON(w, outcome.Status, outcome.Response)
}

func (s *studio) discardProposal(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin edits are not allowed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	if _, ok := s.proposals[id]; !ok {
		studioJSON(w, http.StatusNotFound, map[string]any{"error": "unknown proposal"})
		return
	}
	s.removeProposal(id)
	studioJSON(w, http.StatusOK, map[string]any{"id": id, "discarded": true})
}

func (s *studio) removeProposal(id string) {
	delete(s.proposals, id)
	if s.previewProposal == id {
		s.previewProposal = ""
		s.transport.applyPreview(nil)
	}
}

// applyPreview is the edit-service seam. The engine lane supplies live
// overrides; this stub retains only the last resolved operations.
func (t *studioTransport) applyPreview(ops []edits.PreviewOp) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.previewOps = append([]edits.PreviewOp(nil), ops...)
}
