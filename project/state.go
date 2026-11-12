package project

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"unicode/utf8"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

// EngineBuilder builds the engine configuration of a compiled project at a
// sample rate and block size, with assets when the project plays audio clips
// (nil otherwise), as CompileEngineWithAssets does but without validating the
// project again.
type EngineBuilder func(sampleRate, maxBlock int, assets []engine.AudioAsset) (engine.Config, error)

// StateOptions says how a State reads and checks its project.
type StateOptions struct {
	// Files is where the project's files are read; nil reads the disk.
	Files ProjectFiles
	// Rules applies the host's rules to a project that FromScore compiled
	// and ValidateProject accepted, before the State hands it out: Studio and
	// cicada apply pass the schedule rules (host/schedule.ValidateEngine).
	// build compiles the engine without validating the project again. A
	// non-nil error refuses the state with that error. nil applies none.
	Rules func(p *Project, sources *Sources, build EngineBuilder) error
}

// State is the compiled form of one exact set of source bytes, the
// implementation of edit.State: the entry score's bytes and the bytes of the
// other project files staged in place of their saved ones. It compiles fully,
// as Studio's edit service compiles a candidate (read the project's sources,
// parse, FromScore, the host's rules, the edit plan), and remembers each
// result, so the same bytes are parsed once and each check runs once:
// notation.Validate once (the parse's result stands in for FromScore's),
// ValidateProject once (inside FromScore; the rules and the engine build do
// not repeat it), and each instrument once per declared value, reusing the
// previous state's instruments after With.
//
// Each stage runs on first use: Score parses, Plan and Project compile.
//
// A State never changes once a stage has run. With returns a new state and
// leaves the receiver valid. What its methods return is shared by every
// caller and must not be changed; slices of diagnostics are returned with
// their capacity clipped, so an append copies them. A State is not safe for
// concurrent use.
type State struct {
	path     string // the entry score as the host names it
	absolute string
	source   []byte
	files    map[string][]byte // staged files, by absolute path; not the entry
	opts     StateOptions
	parent   *State
	// instruments remembers each compiled instrument; With hands it on.
	instruments *instrumentCache

	read       bool
	sources    *Sources
	sourcesErr error

	parseDone bool
	parsed    notation.Parsed

	compiled    bool
	project     *Project
	diagnostics []notation.Diagnostic
	compileErr  error
	unread      bool // the compile failed before it had sources
	parseFailed bool // the parse had an error: diagnostics are the parse's

	fileDiagnosticsDone bool
	fileDiagnostics     []notation.Diagnostic

	planDone bool
	plan     *edit.Plan
	planErr  error

	trees map[string]stateTree
}

type stateTree struct {
	node   *gts.Node
	walker *walk.Walker
	err    error
}

var _ edit.State = (*State)(nil)

// NewState returns the state of the score at path with source as its bytes
// and files (absolute path to bytes) in place of the saved bytes of other
// project files. It reads nothing until a method needs it. It copies source
// and files.
func NewState(path string, source []byte, files map[string][]byte, opts StateOptions) *State {
	absolute, _ := filepath.Abs(path)
	staged := make(map[string][]byte, len(files))
	for name, data := range files {
		if name != path && name != absolute {
			staged[name] = bytes.Clone(data)
		}
	}
	return &State{path: path, absolute: absolute, source: bytes.Clone(source), files: staged, opts: opts, instruments: newInstrumentCache(nil)}
}

// NewStateFromSources returns the state of the saved project that sources
// holds, read by the host with source over the entry at path and no other
// override (ReadSources(path, {path: source})). It returns nil when sources
// does not hold source as the entry. It keeps sources, which must not be
// changed afterwards.
func NewStateFromSources(path string, source []byte, sources *Sources, opts StateOptions) *State {
	state := NewState(path, source, nil, opts)
	if sources == nil || !sources.holdsEntry(state.absolute, state.source) {
		return nil
	}
	state.read, state.sources = true, sources
	return state
}

func (s *Sources) holdsEntry(absolute string, source []byte) bool {
	for _, file := range s.Files {
		if file.Path == absolute {
			return bytes.Equal(file.Source, source)
		}
	}
	return false
}

// Source returns the entry score's bytes. They must not be changed.
func (s *State) Source() []byte { return s.source }

// Files returns the staged files by absolute path; empty for the saved
// project. It must not be changed.
func (s *State) Files() map[string][]byte { return s.files }

// readSources reads the project's sources with the state's bytes over the
// saved ones, once.
func (s *State) readSources() (*Sources, error) {
	if !s.read {
		s.read = true
		if s.absolute == "" {
			_, s.sourcesErr = filepath.Abs(s.path)
			if s.sourcesErr == nil {
				s.sourcesErr = fmt.Errorf("cannot resolve %s", s.path)
			}
			return nil, s.sourcesErr
		}
		overrides := make(map[string][]byte, len(s.files)+1)
		maps.Copy(overrides, s.files)
		overrides[s.absolute] = s.source
		files := s.opts.Files
		if files == nil {
			files = OSFiles{}
		}
		s.sources, s.sourcesErr = ReadSourcesIn(files, s.path, overrides)
	}
	return s.sources, s.sourcesErr
}

// Sources returns the project's sources as the state read them. They are
// shared and must not be changed.
func (s *State) Sources() (*Sources, error) { return s.readSources() }

func (s *State) parse() (*notation.Parsed, error) {
	sources, err := s.readSources()
	if err != nil {
		return nil, err
	}
	if !s.parseDone {
		s.parseDone = true
		s.parsed = sources.parseDetailed()
	}
	return &s.parsed, nil
}

// Score returns the lowered project score and its diagnostics, what
// LoadScore returns for the same bytes, from one parse. The score is shared
// and must not be changed.
func (s *State) Score() (*notation.Score, []notation.Diagnostic, error) {
	parsed, err := s.parse()
	if err != nil {
		return nil, nil, err
	}
	return parsed.Score, clip(parsed.Diagnostics), nil
}

// compile runs the compile Studio's edit service runs on a candidate
// (cmd/cicada compileStudioSourceDetailed) on the state's parse, once.
func (s *State) compile() {
	if s.compiled {
		return
	}
	s.compiled = true
	if !utf8.Valid(s.source) {
		s.compileErr, s.unread = fmt.Errorf("score is not UTF-8"), true
		return
	}
	parsed, err := s.parse()
	if err != nil {
		s.compileErr, s.unread = err, true
		return
	}
	score := parsed.Score
	diagnostics := slices.Clone(parsed.Diagnostics)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			s.diagnostics, s.parseFailed = diagnostics, true
			s.compileErr = fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
			return
		}
	}
	p, extra := fromScore(score, parsed, s.instruments)
	diagnostics = append(diagnostics, extra...)
	s.diagnostics = diagnostics
	for _, d := range extra {
		if d.Severity == "error" {
			s.compileErr = fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
			return
		}
	}
	if p == nil {
		s.compileErr = fmt.Errorf("score did not compile")
		return
	}
	// fromScore returns a project only when ValidateProject accepted it, so
	// the service's own ValidateProject call is not repeated here.
	if s.opts.Rules != nil {
		build := func(sampleRate, maxBlock int, assets []engine.AudioAsset) (engine.Config, error) {
			return compileEngineWith(p, sampleRate, maxBlock, assets, nil, compilePlayback, engineInputs{validated: true, instruments: s.instruments})
		}
		if err := s.opts.Rules(p, s.sources, build); err != nil {
			s.compileErr = err
			return
		}
	}
	s.project = p
}

// Project returns the compiled project, or the error Studio's edit service
// gives the same bytes. The project is shared and must not be changed.
func (s *State) Project() (*Project, error) {
	s.compile()
	return s.project, s.compileErr
}

// Detailed returns what Studio's compile of the same bytes returns
// (cmd/cicada compileStudioSourceDetailed): the project, the sources, every
// parse and FromScore diagnostic, and the error. With fileDiagnostics, a
// parse that failed with a score also lists FromScore's diagnostics of that
// score, as Studio's file rows show them. Everything returned is shared and
// must not be changed.
func (s *State) Detailed(fileDiagnostics bool) (*Project, *Sources, []notation.Diagnostic, error) {
	s.compile()
	diagnostics := s.diagnostics
	if fileDiagnostics && s.parseFailed && s.parsed.Score != nil {
		if !s.fileDiagnosticsDone {
			s.fileDiagnosticsDone = true
			_, extra := fromScore(s.parsed.Score, &s.parsed, s.instruments)
			s.fileDiagnostics = append(slices.Clone(s.diagnostics), extra...)
		}
		diagnostics = s.fileDiagnostics
	}
	if s.unread {
		return nil, nil, nil, s.compileErr
	}
	return s.project, s.sources, clip(diagnostics), s.compileErr
}

// Plan returns the edit plan of the compiled project, with the names and
// imports of every source file taken from the state's parse, or the compile's
// error. The plan is shared and must not be changed.
func (s *State) Plan() (*edit.Plan, error) {
	if !s.planDone {
		s.planDone = true
		p, err := s.Project()
		if err != nil {
			s.planErr = err
		} else {
			s.plan = editPlan(p, s.source, s.sources.Files, s.parsed.Outlines)
		}
	}
	return s.plan, s.planErr
}

// fileBytes returns the state's bytes of one project file.
func (s *State) fileBytes(path string) ([]byte, bool) {
	if path == s.path || path == s.absolute {
		return s.source, true
	}
	if data, ok := s.files[path]; ok {
		return data, true
	}
	if sources, err := s.readSources(); err == nil {
		for _, file := range sources.Files {
			if file.Path == path {
				return file.Source, true
			}
		}
	}
	return nil, false
}

// Tree returns the syntax tree of one file of the state, parsed on first
// use. The tree is shared and must not be changed.
func (s *State) Tree(path string) (*gts.Node, *walk.Walker, error) {
	if tree, ok := s.trees[path]; ok {
		return tree.node, tree.walker, tree.err
	}
	data, ok := s.fileBytes(path)
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a source of this project", path)
	}
	node, walker, err := notation.ParseTree(data)
	if s.trees == nil {
		s.trees = map[string]stateTree{}
	}
	s.trees[path] = stateTree{node: node, walker: walker, err: err}
	return node, walker, err
}

// Offset converts a position in one file of the state to a byte offset, as
// edit.Offset does.
func (s *State) Offset(path string, at notation.Position) (int, error) {
	data, ok := s.fileBytes(path)
	if !ok {
		return 0, fmt.Errorf("%s is not a source of this project", path)
	}
	return edit.Offset(data, path, at)
}

// With returns the state of the bytes after replacing files (absolute path
// to bytes; the entry by its path). It compiles them from scratch on first
// use, reusing the instruments this state compiled. The receiver stays valid.
func (s *State) With(files map[string][]byte) (edit.State, error) {
	return s.with(files), nil
}

func (s *State) with(files map[string][]byte) *State {
	next := &State{path: s.path, absolute: s.absolute, source: s.source, files: maps.Clone(s.files), opts: s.opts, parent: s, instruments: newInstrumentCache(s.instruments)}
	for name, data := range files {
		if name == s.path || name == s.absolute {
			next.source = bytes.Clone(data)
		} else {
			next.files[name] = bytes.Clone(data)
		}
	}
	return next
}

// Rollback returns the state With was called on.
func (s *State) Rollback() (edit.State, error) {
	if s.parent == nil {
		return nil, errors.New("project: no earlier state to roll back to")
	}
	return s.parent, nil
}

// Kept returns the state a host keeps after it committed these bytes: the
// same compile, its staged files now saved (Files is empty), with no link to
// the states it was derived from, so they can be collected. Rollback fails
// on it.
func (s *State) Kept() *State {
	kept := *s
	kept.files, kept.parent = map[string][]byte{}, nil
	kept.instruments = &instrumentCache{entries: s.instruments.entries}
	return &kept
}

// SelfContained reports whether the state's compile read nothing but the
// project's source files, so that a host which finds the same sources on
// disk again may keep using it: no imports (whose pins are read from
// cicada.sum), and no assets, clips or samplers (whose audio files and sample
// packs the parse and the rules read). It reads the sources and parses.
func (s *State) SelfContained() bool {
	parsed, err := s.parse()
	if err != nil {
		return false
	}
	if len(s.sources.Imports) > 0 || len(s.sources.Libraries) > 0 {
		return false
	}
	score := parsed.Score
	return score != nil && len(score.Assets) == 0 && len(score.Clips) == 0 && len(score.Samplers) == 0
}

// SameSources reports whether sources, read by the host just now, holds the
// same project as the state read: the same manifest and the same bytes of
// every file. It reads the state's sources if it has not.
func (s *State) SameSources(sources *Sources) bool {
	mine, err := s.readSources()
	if err != nil || sources == nil {
		return false
	}
	if mine.Root != sources.Root || mine.ManifestPath != sources.ManifestPath || len(mine.Files) != len(sources.Files) {
		return false
	}
	for i := range mine.Files {
		a, b := mine.Files[i], sources.Files[i]
		if a.Path != b.Path || a.Library != b.Library || a.Root != b.Root || a.Edition != b.Edition || !bytes.Equal(a.Source, b.Source) ||
			!maps.Equal(a.Bindings, b.Bindings) || !maps.Equal(a.Declarations, b.Declarations) {
			return false
		}
	}
	return reflect.DeepEqual(mine.Manifest, sources.Manifest) && slices.Equal(mine.LibraryOrder, sources.LibraryOrder) &&
		reflect.DeepEqual(mine.Libraries, sources.Libraries) && reflect.DeepEqual(mine.Imports, sources.Imports) && reflect.DeepEqual(mine.Bindings, sources.Bindings)
}

// clip returns diagnostics with their capacity cut to their length, so a
// caller's append copies them instead of writing into the state's array.
func clip(diagnostics []notation.Diagnostic) []notation.Diagnostic {
	return diagnostics[:len(diagnostics):len(diagnostics)]
}
