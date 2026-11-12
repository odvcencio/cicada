package applybench

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Session is one reference score on disk, as Studio serves it: a loose score
// file in a folder of its own, with no manifest and no libraries.
type Session struct {
	Path   string // absolute path of the score
	Source []byte // the score as written
}

// NewSession writes source as score.cicada in dir.
func NewSession(dir string, source []byte) (*Session, error) {
	path, err := filepath.Abs(filepath.Join(dir, "score.cicada"))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, source, 0o600); err != nil {
		return nil, err
	}
	return &Session{Path: path, Source: source}, nil
}

// Compiler is the edit service's compiler (cmd/cicada/studio_intents.go,
// studioCompiler): an edit.StateCompiler whose states compile each set of
// bytes once, as the service's do (project.State with the schedule rules,
// schedule.StateRules). It keeps the state of its latest successful compile,
// which is the state an Apply hands on, and counts its compiles: every plan a
// state is asked for, as the service's studioCompile seam sees them.
type Compiler struct {
	path     string
	Last     *State
	Compiles int
}

// StateOptions are the options of the service's states.
var StateOptions = project.StateOptions{Rules: schedule.StateRules}

func (c *Compiler) Compile(source []byte, files map[string][]byte) (*edit.Plan, error) {
	return compilerState{project.NewState(c.path, source, files, StateOptions), c}.Plan()
}

// State is the state of source with files staged (edit.StateCompiler).
func (c *Compiler) State(source []byte, files map[string][]byte) (edit.State, error) {
	return compilerState{project.NewState(c.path, source, files, StateOptions), c}, nil
}

// KeptState returns the compiled state of source at path as the service
// keeps it between edits (cmd/cicada keptState): built on the sources the
// service reads, and compiled.
func KeptState(path string, source []byte) (*project.State, error) {
	sources, err := project.ReadSources(path, map[string][]byte{path: source})
	if err != nil {
		return nil, err
	}
	state := project.NewStateFromSources(path, source, sources, StateOptions)
	if state == nil {
		return nil, fmt.Errorf("the sources do not hold the score")
	}
	if _, err := state.Plan(); err != nil {
		return nil, err
	}
	return state, nil
}

// WithBase sets opts.Base to kept, read through compiler as the service reads
// its kept state through each request's compiler.
func WithBase(opts *edit.Options, compiler *Compiler, kept *project.State) {
	if kept != nil {
		opts.Base = compilerState{kept, compiler}
	}
}

// compilerState is a project.State whose plans the Compiler counts and
// records, as the service's studioState does.
type compilerState struct {
	*project.State
	compiler *Compiler
}

func (s compilerState) Plan() (*edit.Plan, error) {
	s.compiler.Compiles++
	plan, err := s.State.Plan()
	if err != nil {
		return nil, err
	}
	state, err := StateOf(s.State)
	if err != nil {
		return nil, err
	}
	s.compiler.Last = state
	return plan, nil
}

func (s compilerState) With(files map[string][]byte) (edit.State, error) {
	next, err := s.State.With(files)
	if err != nil {
		return nil, err
	}
	return compilerState{next.(*project.State), s.compiler}, nil
}

func (s compilerState) Rollback() (edit.State, error) {
	previous, err := s.State.Rollback()
	if err != nil {
		return nil, err
	}
	return compilerState{previous.(*project.State), s.compiler}, nil
}

// Hooks counts the host callbacks one Apply makes besides compiles.
type Hooks struct {
	ProjectParses int // Options.ParseProject: a project parse an intent asked for
	Diagnoses     int // Options.Diagnose: cicada check's full inspection
}

// Options returns the options Studio's edit service builds for source, the
// current bytes of the score (cmd/cicada/studio_intents.go:125-189,
// editOptions), and the compiler inside them. Those options are built in
// package main, so this is the closest public path, with three differences:
// RenderCheck and UpgradeEdition are nil (no hot edit renders audio or
// upgrades an edition-1 score; an intent that needs them refuses), and the
// mixer and capture guards always allow (no recording is open). Diagnose is
// cicada check's inspection (cmd/cicada/check.go, inspectSources), rebuilt
// from the same public calls.
func (s *Session) Options(source []byte, hooks *Hooks) (edit.Options, *Compiler, error) {
	editionNumber, manifestPath, err := edition.ScoreEdition(s.Path)
	if err != nil {
		return edit.Options{}, nil, err
	}
	if manifestPath == "" {
		editionNumber = 0 // a loose file's header decides
	}
	sources, err := project.ReadSources(s.Path, map[string][]byte{s.Path: source})
	if err != nil {
		return edit.Options{}, nil, err
	}
	compiler := &Compiler{path: s.Path}
	allow := func() error { return nil }
	opts := edit.Options{
		Compiler: compiler, Path: s.Path, Edition: editionNumber, ManifestPath: manifestPath,
		Sources: sources.Files, Now: time.Now, MixerGuard: allow, CaptureGuard: allow,
	}
	opts.Diagnose = func(candidate []byte, files map[string][]byte) ([]notation.Diagnostic, error) {
		hooks.Diagnoses++
		staged := make(map[string][]byte, len(files)+1)
		for path, data := range files {
			staged[path] = data
		}
		staged[s.Path] = candidate
		return inspect(s.Path, staged)
	}
	opts.ParseProject = func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
		hooks.ProjectParses++
		if files == nil {
			files = make(map[string][]byte)
		}
		files[s.Path] = source
		return project.LoadScore(s.Path, files)
	}
	return opts, compiler, nil
}

// inspect returns every diagnostic cicada check gives the project with the
// staged bytes (cmd/cicada/check.go, inspectScoreWith and inspectSources).
func inspect(path string, staged map[string][]byte) ([]notation.Diagnostic, error) {
	sources, err := project.ReadSources(path, staged)
	if err != nil {
		return nil, err
	}
	score, diagnostics := sources.Parse()
	if !hasErrors(diagnostics) {
		semantic, more := project.FromScore(score)
		diagnostics = appendUnique(diagnostics, more)
		if semantic != nil && !semantic.NeedsSampleEngine() {
			if _, err := project.CompileEngine(semantic, 48_000, 128); err != nil {
				diagnostics = append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
			}
		}
	}
	sort.SliceStable(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Position.Line != b.Position.Line {
			return a.Position.Line < b.Position.Line
		}
		if a.Position.Column != b.Position.Column {
			return a.Position.Column < b.Position.Column
		}
		if a.Severity != b.Severity {
			return a.Severity == "error"
		}
		return a.Code < b.Code
	})
	return diagnostics, nil
}

func hasErrors(diagnostics []notation.Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

func appendUnique(existing, extra []notation.Diagnostic) []notation.Diagnostic {
	seen := make(map[notation.Diagnostic]bool, len(existing)+len(extra))
	for _, d := range existing {
		seen[d] = true
	}
	for _, d := range extra {
		if !seen[d] {
			existing = append(existing, d)
			seen[d] = true
		}
	}
	return existing
}
