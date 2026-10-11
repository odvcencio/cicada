package lsp

import (
	"fmt"
	"path/filepath"
	"sync"
	"unicode/utf8"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

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
	if c.byRevision == nil {
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
