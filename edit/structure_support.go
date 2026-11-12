package edit

import (
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
)

// staged holds a structural edit's new bytes for each source file it
// changes: the entry (under ctx.Options.Path) and other files of the
// project. Library files are never staged.
type staged struct {
	ctx   *Context
	files map[string][]byte
}

func newStaged(ctx *Context) *staged { return &staged{ctx: ctx, files: map[string][]byte{}} }

// source returns a file's bytes as the edit has left them so far.
func (s *staged) source(path string) []byte {
	if data, ok := s.files[path]; ok {
		return data
	}
	data, _ := s.ctx.FileSource(path)
	return data
}

func (s *staged) set(path string, data []byte) { s.files[path] = data }

func (s *staged) entry() []byte { return s.source(s.ctx.Options.Path) }

// overrides are the files a compile of the staged edit reads beside the entry.
func (s *staged) overrides() map[string][]byte {
	files := s.ctx.fileOverrides()
	for path, data := range s.files {
		if path != s.ctx.Options.Path {
			files[path] = data
		}
	}
	return files
}

// commit writes the staged bytes into the context: the entry as its source,
// other files as staged files of the batch.
func (s *staged) commit() {
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path == s.ctx.Options.Path {
			s.ctx.Source = s.files[path]
			continue
		}
		before, _ := s.ctx.FileSource(path)
		s.ctx.AddFile(File{Path: path, Before: before, After: s.files[path]})
	}
	s.ctx.plan = nil
}

// sourceFile is one source of the project, the entry first.
type sourceFile struct {
	path    string
	library bool
}

func projectFiles(ctx *Context) []sourceFile {
	files := []sourceFile{{path: ctx.Options.Path}}
	for _, file := range ctx.Options.Sources {
		if file.Path != ctx.Options.Path {
			files = append(files, sourceFile{path: file.Path, library: file.Library != ""})
		}
	}
	return files
}

// fileUses is one source file's uses of a name.
type fileUses struct {
	sourceFile
	uses []notation.NameUse
}

// projectUses finds a track's, a pattern's or a scene's declaration and uses
// in every source file of the project, as the staged edit has left them.
// Library files cannot name a project's tracks, patterns or scenes, and an
// entity they declare has a qualified name, so they are left out.
func projectUses(st *staged, kind, name string) ([]fileUses, error) {
	var found []fileUses
	for _, file := range projectFiles(st.ctx) {
		if file.library {
			continue
		}
		all, err := notation.NameUses(st.source(file.path))
		if err != nil {
			return nil, err
		}
		if uses := notation.UsesOf(all, kind, name); len(uses) > 0 {
			found = append(found, fileUses{sourceFile: file, uses: uses})
		}
	}
	return found, nil
}

// declarationIn names the file that declares the entity among found.
func declarationIn(found []fileUses) (string, bool) {
	for _, file := range found {
		for _, use := range file.uses {
			if use.Role == "declaration" {
				return file.path, true
			}
		}
	}
	return "", false
}

// describe names a use for a refusal, with its file when it is not the entry.
func describe(ctx *Context, path string, use notation.NameUse) string {
	if path == ctx.Options.Path {
		return use.Use
	}
	return strings.Replace(use.Use, fmt.Sprintf("(line %d)", use.Line), fmt.Sprintf("(%s line %d)", projectPath(ctx, path), use.Line), 1)
}

// projectPath names a source file relative to the project folder (the
// manifest's, or the entry's when there is none), with forward slashes.
func projectPath(ctx *Context, path string) string {
	base := ctx.Options.Path
	if ctx.Options.ManifestPath != "" {
		base = ctx.Options.ManifestPath
	}
	root, err := filepath.Abs(filepath.Dir(base))
	if err != nil {
		return path
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if relative, err := filepath.Rel(root, absolute); err == nil && !strings.HasPrefix(relative, "..") {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(path)
}

// diagnosticText names a diagnostic for a refusal: its message without the
// code, its file when it is not the entry, and its line.
func diagnosticText(ctx *Context, d notation.Diagnostic) string {
	switch {
	case d.Position.Line == 0:
		return d.Text()
	case d.Position.File != "" && d.Position.File != ctx.Options.Path:
		return fmt.Sprintf("%s (%s line %d)", d.Text(), projectPath(ctx, d.Position.File), d.Position.Line)
	}
	return fmt.Sprintf("%s (line %d)", d.Text(), d.Position.Line)
}

// stagedDiagnostics returns the project's diagnostics with the staged bytes:
// with the host's Diagnose hook every one cicada check gives, else the parse
// diagnostics and, when they have no error, a compile error.
func stagedDiagnostics(st *staged) ([]notation.Diagnostic, error) {
	ctx := st.ctx
	if ctx.Options.Diagnose != nil {
		return ctx.Options.Diagnose(st.entry(), st.overrides())
	}
	working := *ctx
	working.Source, working.plan = st.entry(), nil
	working.files = append([]File(nil), ctx.files...)
	working.fileIndex = maps.Clone(ctx.fileIndex)
	for path, data := range st.files {
		if path != ctx.Options.Path {
			before, _ := ctx.FileSource(path)
			working.AddFile(File{Path: path, Before: before, After: data})
		}
	}
	_, ds, err := working.ParseProject()
	if err != nil {
		return nil, err
	}
	if !hasErrors(ds) {
		if _, err := ctx.Options.Compiler.Compile(st.entry(), st.overrides()); err != nil {
			ds = append(ds, notation.Diagnostic{Severity: "error", Message: err.Error()})
		}
	}
	return ds, nil
}

func errorsOf(ds []notation.Diagnostic) []notation.Diagnostic {
	var errs []notation.Diagnostic
	for _, d := range ds {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	return errs
}

// resultValid refuses a structural edit whose staged result would not
// validate, with reason result_invalid and every problem in uses.
func resultValid(st *staged, field, what string) error {
	ds, err := stagedDiagnostics(st)
	if err != nil {
		return err
	}
	errs := errorsOf(ds)
	if len(errs) == 0 {
		return nil
	}
	uses := make([]string, 0, len(errs))
	for _, d := range errs {
		uses = append(uses, diagnosticText(st.ctx, d))
	}
	return &ReasonError{Reason: "result_invalid", Field: field, Uses: uses, Message: fmt.Sprintf("%s leaves the score invalid: %s", what, strings.Join(uses, "; "))}
}

// checkReseed compares the compiled plan before and after a staged edit.
// The engine seeds chance (x?50) and humanize by a track's position and a
// pattern's slot on its track (kernel/seq/schedule.go, ProbabilityHit and
// HumanizeVelocity), so an edit that moves a track, or a pattern's slot,
// of a track that plays such a pattern changes those random choices. Without
// reseed the edit is refused with reason reseeds_random_choices and the
// tracks and patterns in uses; with reseed it goes ahead and the result's
// warnings name them.
func checkReseed(st *staged, reseed bool) error {
	ctx := st.ctx
	before, err := ctx.CurrentPlan()
	if err != nil || before == nil {
		return nil
	}
	after, err := ctx.Options.Compiler.Compile(st.entry(), st.overrides())
	if err != nil || after == nil {
		return nil
	}
	score, _, _ := ctx.ParseProject()
	seeded := seededPatterns(after, score)
	beforeIndex := map[string]int{}
	for i, track := range before.Tracks {
		beforeIndex[track.ID] = i
	}
	var moved []string
	for i, track := range after.Tracks {
		j, ok := beforeIndex[track.ID]
		if !ok {
			continue
		}
		for slot, pattern := range track.Slots {
			if pattern == nil || !seeded[*pattern] {
				continue
			}
			old := -1
			for k, earlier := range before.Tracks[j].Slots {
				if earlier != nil && *earlier == *pattern {
					old = k
				}
			}
			switch {
			case i != j:
				moved = append(moved, fmt.Sprintf("track %s, pattern %s: the track moves from position %d to %d", track.ID, *pattern, j+1, i+1))
			case old >= 0 && old != slot:
				moved = append(moved, fmt.Sprintf("track %s, pattern %s: the pattern moves from slot %d to %d", track.ID, *pattern, old+1, slot+1))
			}
		}
	}
	if len(moved) == 0 {
		return nil
	}
	if !reseed {
		return &ReasonError{Reason: "reseeds_random_choices", Field: "reseed", Uses: moved, Message: fmt.Sprintf("this edit changes the random choices (chance and humanize) of %s, because the engine seeds them by track position and pattern slot; give reseed to accept that", strings.Join(moved, "; "))}
	}
	for _, change := range moved {
		ctx.warn("random choices (chance and humanize) change: %s", change)
	}
	return nil
}

// seededPatterns are the patterns whose playing depends on a seed: a step
// with a chance between 0 and 100, or humanize.
func seededPatterns(plan *Plan, score *notation.Score) map[string]bool {
	seeded := map[string]bool{}
	chance := func(steps []*Step) bool {
		for _, step := range steps {
			if step != nil && step.Probability > 0 && step.Probability < 100 {
				return true
			}
		}
		return false
	}
	for _, pattern := range plan.Patterns {
		if chance(pattern.Data) {
			seeded[pattern.ID] = true
		}
		for _, lane := range pattern.Lanes {
			if chance(lane) {
				seeded[pattern.ID] = true
			}
		}
	}
	if score != nil {
		for _, pattern := range score.Patterns {
			for _, attr := range pattern.Attrs {
				if attr.Name == "humanize" && strings.TrimSpace(attr.Value) != "0" && strings.TrimSpace(attr.Value) != "0%" {
					seeded[pattern.Name] = true
				}
			}
		}
	}
	return seeded
}
