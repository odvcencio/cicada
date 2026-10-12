package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/rendercheck"
	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// newRenderCheck builds the render check for one score; tests replace it.
var newRenderCheck = func(path string) edits.RenderCheck { return rendercheck.New(path) }

// lspCompiler compiles the working entry and auxiliary buffers without writes.
type lspCompiler struct{ path string }

func (c lspCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("score is not UTF-8")
	}
	path, err := filepath.Abs(c.path)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string][]byte, len(files)+1)
	for path, data := range files {
		overrides[path] = data
	}
	overrides[path] = source
	score, ds, err := project.LoadScore(path, overrides)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
		}
	}
	if p == nil {
		return nil, fmt.Errorf("score did not compile")
	}
	if err := project.ValidateProject(p); err != nil {
		return nil, err
	}
	sources, err := project.ReadSources(path, overrides)
	if err != nil {
		return nil, err
	}
	return project.EditPlan(p, source, sources.Files...), nil
}

type hashResult struct {
	hash string
	err  error
}

const cachedCheckLimit = 64

// cachedCheck retains render results for the lifetime of one check instance.
type cachedCheck struct {
	inner      edits.RenderCheck
	mu         sync.Mutex
	byRevision map[string]hashResult
}

func (c *cachedCheck) Hash(source []byte) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner == nil {
		return "", edits.ErrNoRenderCheck
	}
	revision := edits.Revision(source)
	if result, ok := c.byRevision[revision]; ok {
		return result.hash, result.err
	}
	hash, err := c.inner.Hash(source)
	if c.byRevision == nil || len(c.byRevision) >= cachedCheckLimit {
		c.byRevision = make(map[string]hashResult)
	}
	c.byRevision[revision] = hashResult{hash: hash, err: err}
	return hash, err
}

type textEdit struct {
	Range   region `json:"range"`
	NewText string `json:"newText"`
}

// minimalTextEdit replaces the differing byte window with one UTF-16 LSP edit.
// Keep rune and CRLF boundaries intact so editors can apply the range exactly.
func minimalTextEdit(before, after []byte) textEdit {
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	for start > 0 && start < len(before) && !utf8.RuneStart(before[start]) {
		start--
	}
	if start > 0 && ((start < len(before) && before[start-1] == '\r' && before[start] == '\n') || (start < len(after) && after[start-1] == '\r' && after[start] == '\n')) {
		start--
	}
	oldEnd, newEnd := len(before), len(after)
	for oldEnd > start && newEnd > start && before[oldEnd-1] == after[newEnd-1] {
		oldEnd--
		newEnd--
	}
	for oldEnd < len(before) && !utf8.RuneStart(before[oldEnd]) {
		oldEnd++
		newEnd++
	}
	if (oldEnd > 0 && oldEnd < len(before) && before[oldEnd-1] == '\r' && before[oldEnd] == '\n') || (newEnd > 0 && newEnd < len(after) && after[newEnd-1] == '\r' && after[newEnd] == '\n') {
		oldEnd++
		newEnd++
	}
	return textEdit{Range: region{Start: utf16Position(before, start), End: utf16Position(before, oldEnd)}, NewText: string(after[start:newEnd])}
}

// intentOptions supplies the project inputs for an edit-service call on the
// open document. Other files come from disk; only the open buffer is replaced.
func (s *server) intentOptions(path string, source []byte) (edits.Options, error) {
	projectEdition, manifestPath, err := edition.ScoreEdition(path)
	if err != nil {
		return edits.Options{}, err
	}
	// ScoreEdition's loose-file default is a fallback; let Parse read the header.
	if manifestPath == "" {
		projectEdition = 0
	}
	overrides := map[string][]byte{path: source}
	sources, err := project.ReadSources(path, overrides)
	if err != nil {
		return edits.Options{}, err
	}
	opts := edits.Options{Compiler: lspCompiler{path: path}, RenderCheck: s.renderCheck(path), Path: path, Edition: projectEdition, ManifestPath: manifestPath, Sources: sources.Files, Now: time.Now}
	if manifestPath != "" {
		if opts.Manifest, err = os.ReadFile(manifestPath); err != nil {
			return edits.Options{}, err
		}
		manifest, err := edition.ParseProjectManifest(opts.Manifest)
		if err != nil {
			return edits.Options{}, err
		}
		opts.Edition = manifest.Edition
	}
	opts.ParseProject = func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
		merged := make(map[string][]byte, len(files)+1)
		for name, data := range files {
			merged[name] = data
		}
		merged[path] = source
		return project.LoadScore(path, merged)
	}
	return opts, nil
}

// renderCheck returns the per-score cached check, so renders are shared by
// every code-action request at one revision.
func (s *server) renderCheck(path string) *cachedCheck {
	if s.checks == nil {
		s.checks = make(map[string]*cachedCheck)
	}
	check := s.checks[path]
	if check == nil {
		check = &cachedCheck{inner: newRenderCheck(path)}
		s.checks[path] = check
	}
	return check
}

// intentActions are the code actions backed by edit intents. Each runs
// edits.Apply on the open buffer and offers the result only if it succeeds.
func (s *server) intentActions(uri, path string, source []byte, params codeActionParams) []any {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	var actions []any
	if params.wants("quickfix") {
		actions = append(actions, s.snapActions(uri, path, source, params)...)
	}
	if params.wants("refactor") {
		if action := s.convertAction(uri, path, source, params); action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

func (s *server) intentAction(uri, title, kind string, before, after []byte) map[string]any {
	return map[string]any{
		"title": title, "kind": kind,
		"edit": map[string]any{"documentChanges": []any{map[string]any{
			"textDocument": map[string]any{"uri": uri, "version": s.versions[uri]},
			"edits":        []any{minimalTextEdit(before, after)},
		}}},
	}
}

// snapActions answers the "must start on a step" diagnostic with a MoveClip to
// the nearest step.
func (s *server) snapActions(uri, path string, source []byte, params codeActionParams) []any {
	var lines []int
	for _, d := range params.Context.Diagnostics {
		if d.Code == "CICADA-ARRANGEMENT" && strings.Contains(d.Message, "must start on a step") {
			lines = append(lines, d.Range.Start.Line+1)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	score, _, err := project.LoadScore(path, map[string][]byte{path: source})
	if err != nil || score == nil || score.Arrange == nil {
		return nil
	}
	opts, err := s.intentOptions(path, source)
	if err != nil {
		return nil
	}
	var actions []any
	seen := map[string]bool{}
	for _, line := range lines {
		for _, p := range score.Arrange.Placements {
			if p.Position.Line != line || seen[p.Name] || p.AtTick%seq.TicksPerStep == 0 {
				continue
			}
			seen[p.Name] = true
			snapped := (p.AtTick + seq.TicksPerStep/2) / seq.TicksPerStep * seq.TicksPerStep
			env := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.MoveClip{Entity: edits.EntityID("placement:" + p.Name), AtTick: snapped}}}
			result, err := edits.Apply(source, env, opts)
			if err != nil || result.Unchanged {
				continue
			}
			actions = append(actions, s.intentAction(uri, "Snap placement to the step grid", "quickfix", source, result.Source))
		}
	}
	return actions
}

// convertAction offers the song-to-arrange refactor when the cursor is in the
// song block and the render check shows the conversion keeps the music. The
// check renders audio, so the block gate keeps it off the cursor-move path.
func (s *server) convertAction(uri, path string, source []byte, params codeActionParams) map[string]any {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil
	}
	inSong := false
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) != "song_decl" {
			continue
		}
		first := utf16Position(source, int(node.StartByte())).Line
		last := utf16Position(source, int(node.EndByte())).Line
		inSong = params.Range.Start.Line <= last && params.Range.End.Line >= first
		break
	}
	if !inSong {
		return nil
	}
	opts, err := s.intentOptions(path, source)
	if err != nil {
		return nil
	}
	env := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
	result, err := edits.Apply(source, env, opts)
	if err != nil || result.Unchanged {
		return nil
	}
	return s.intentAction(uri, "Convert song to arrange", "refactor", source, result.Source)
}
