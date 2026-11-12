package edit

import (
	"bytes"
	"errors"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

// State is the compiled form of one exact set of source bytes: the entry
// score (Source) and the other project files a batch has staged in place of
// their saved bytes (Files). An edit reads the unchanged score from it instead
// of parsing and compiling again, and moves to the state of its result with
// With. project.State implements it; edit cannot import project, which
// imports edit, so edit sees it through this interface, as it sees the
// compiler.
//
// What a State returns belongs to the State and is shared by every reader:
// callers must not change it.
type State interface {
	// Source is the entry score's bytes. Files holds the other files the
	// state was built with in place of their saved bytes, by absolute path;
	// it is empty for a state of the saved project.
	Source() []byte
	Files() map[string][]byte
	// Score is the lowered project score and its diagnostics, as
	// Options.ParseProject returns them for these bytes.
	Score() (*notation.Score, []notation.Diagnostic, error)
	// Tree is the syntax tree of one file of the state.
	Tree(path string) (*gts.Node, *walk.Walker, error)
	// Plan is the compiled and validated edit plan, or the error
	// Compiler.Compile returns for these bytes, with the same text.
	Plan() (*Plan, error)
	// Offset converts a position in one file of the state to a byte offset,
	// as the function Offset does.
	Offset(path string, at notation.Position) (int, error)
	// With returns the state of the bytes after replacing these files (the
	// entry among them, by its path). It reuses what the change leaves alone.
	// The receiver must not be read afterwards unless the returned state is
	// rolled back (an implementation may promise more: project.State never
	// changes).
	With(files map[string][]byte) (State, error)
	// Rollback returns the state With was called on.
	Rollback() (State, error)
}

// StateCompiler is a Compiler that also builds states. Apply builds the state
// of its source through it when Options.Base is nil.
type StateCompiler interface {
	Compiler
	// State returns the state of source with files in place of the saved
	// bytes of those project files. Its Plan must equal Compile(source,
	// files), and its Score what Options.ParseProject returns for the same
	// bytes. A nil State with a nil error means the compiler keeps no states
	// for these options; Apply then compiles through Compile.
	State(source []byte, files map[string][]byte) (State, error)
}

// errNoState says a Context has no state: the compiler keeps none.
var errNoState = errors.New("edit: no compiled state")

// stateChain is the state of the bytes an Apply last read, shared by every
// copy of its Context. Each read compares the context's bytes with the
// state's and moves to the state of the new bytes when they differ, so a state
// is only ever read for the exact bytes it was built from.
type stateChain struct {
	path     string // the entry score, Options.Path of the Apply
	compiler StateCompiler
	base     State // Options.Base, or the state built for the source of Apply
	current  State // the state of the bytes last read
	steps    int   // With calls from base to current, for rollback
	none     bool  // the compiler keeps no states
}

func newStateChain(opts Options) *stateChain {
	compiler, _ := opts.Compiler.(StateCompiler)
	if opts.Base == nil && compiler == nil {
		return nil
	}
	return &stateChain{path: opts.Path, compiler: compiler, base: opts.Base, current: opts.Base}
}

// at returns the state of the entry bytes source with the staged files.
func (s *stateChain) at(source []byte, files []File) (State, error) {
	if s == nil || s.none {
		return nil, errNoState
	}
	if s.current == nil {
		if s.compiler == nil {
			s.none = true
			return nil, errNoState
		}
		state, err := s.compiler.State(source, stagedBytes(files))
		if err != nil {
			return nil, err
		}
		if state == nil {
			s.none = true
			return nil, errNoState
		}
		s.base, s.current = state, state
		return state, nil
	}
	changed, ok := stateChanges(s.current, s.path, source, files)
	if !ok {
		// The state holds a staged file the context does not: a copy of the
		// context from before that file was staged. Build that state afresh.
		if s.compiler == nil {
			return nil, errNoState
		}
		state, err := s.compiler.State(source, stagedBytes(files))
		if err != nil || state == nil {
			return nil, errNoState
		}
		return state, nil
	}
	if len(changed) == 0 {
		return s.current, nil
	}
	next, err := s.current.With(changed)
	if err != nil {
		return nil, err
	}
	s.current = next
	s.steps++
	return next, nil
}

// rollback returns the chain to its base, for an Apply that failed: the host
// may keep using Options.Base.
func (s *stateChain) rollback() {
	if s == nil {
		return
	}
	for ; s.steps > 0 && s.current != nil; s.steps-- {
		previous, err := s.current.Rollback()
		if err != nil {
			break
		}
		s.current = previous
	}
}

// stateChanges returns the files whose bytes differ between state and the
// context's bytes (the entry by its path). ok is false when the state holds
// a staged file the context lacks, which With cannot express.
func stateChanges(state State, path string, source []byte, files []File) (changed map[string][]byte, ok bool) {
	held := state.Files()
	matched := 0
	for _, file := range files {
		if file.Path == path {
			continue
		}
		data, found := held[file.Path]
		if found {
			matched++
		}
		if !found || !bytes.Equal(data, file.After) {
			if changed == nil {
				changed = map[string][]byte{}
			}
			changed[file.Path] = file.After
		}
	}
	if matched != len(held) {
		return nil, false
	}
	if !bytes.Equal(state.Source(), source) {
		if changed == nil {
			changed = map[string][]byte{}
		}
		changed[path] = source
	}
	return changed, true
}

func stagedBytes(files []File) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for _, file := range files {
		out[file.Path] = file.After
	}
	return out
}
