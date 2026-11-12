package scoreengine

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/schedule/rules"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// compiler is Studio's pre-write check on files held in memory, in the order
// of cmd/cicada's compileStudioSourceDetailed: UTF-8, the project's sources,
// parse, conversion, validation, the schedule rules, then the edit plan.
type compiler struct {
	files project.ProjectFiles
	path  string // the entry, as project code names it
	packs rules.Packs

	// The plan of the last compile, and what it was compiled from, so
	// Context.CurrentPlan of the next Apply on the same text costs nothing.
	source  []byte
	staged  map[string][]byte
	plan    *edits.Plan
	project *project.Project
}

func (c *compiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	if c.plan != nil && bytes.Equal(source, c.source) && sameFiles(files, c.staged) {
		return c.plan, nil
	}
	p, sources, err := compileProject(c.files, c.path, source, files, c.packs)
	if err != nil {
		return nil, err
	}
	plan := project.EditPlan(p, source, sources.Files...)
	c.source, c.staged, c.plan, c.project = bytes.Clone(source), cloneFiles(files), plan, p
	return plan, nil
}

// compileProject compiles the entry source over the project's files with the
// staged files laid over them, and applies Studio's checks.
func compileProject(files project.ProjectFiles, path string, source []byte, staged map[string][]byte, packs rules.Packs) (*project.Project, *project.Sources, error) {
	if !utf8.Valid(source) {
		return nil, nil, fmt.Errorf("score is not UTF-8")
	}
	overrides := cloneFiles(staged)
	overrides[path] = source
	sources, err := project.ReadSourcesIn(files, path, overrides)
	if err != nil {
		return nil, nil, err
	}
	score, diagnostics := sources.Parse()
	if err := firstError(diagnostics); err != nil {
		return nil, sources, err
	}
	p, extra := project.FromScore(score)
	if err := firstError(extra); err != nil {
		return nil, sources, err
	}
	if p == nil {
		return nil, sources, fmt.Errorf("score did not compile")
	}
	if err := project.ValidateProject(p); err != nil {
		return nil, sources, err
	}
	if err := rules.Validate(p, 48000, 128, packs); err != nil {
		return nil, sources, err
	}
	return p, sources, nil
}

func firstError(diagnostics []notation.Diagnostic) error {
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
		}
	}
	return nil
}

func cloneFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files)+1)
	for path, data := range files {
		out[path] = bytes.Clone(data)
	}
	return out
}

func sameFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for path, data := range a {
		other, ok := b[path]
		if !ok || !bytes.Equal(data, other) {
			return false
		}
	}
	return true
}
