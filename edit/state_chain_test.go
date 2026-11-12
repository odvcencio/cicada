package edit

import (
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

// bytesState is a State that only knows its bytes.
type bytesState struct {
	source []byte
	files  map[string][]byte
	parent *bytesState
}

func (s *bytesState) Source() []byte           { return s.source }
func (s *bytesState) Files() map[string][]byte { return s.files }
func (s *bytesState) Score() (*notation.Score, []notation.Diagnostic, error) {
	return nil, nil, nil
}
func (s *bytesState) Tree(string) (*gts.Node, *walk.Walker, error) { return nil, nil, nil }
func (s *bytesState) Plan() (*Plan, error)                          { return &Plan{}, nil }
func (s *bytesState) Offset(string, notation.Position) (int, error) { return 0, nil }
func (s *bytesState) Rollback() (State, error)                      { return s.parent, nil }
func (s *bytesState) With(files map[string][]byte) (State, error) {
	next := &bytesState{source: s.source, files: map[string][]byte{}, parent: s}
	for path, data := range s.files {
		next.files[path] = data
	}
	for path, data := range files {
		if path == "/entry" {
			next.source = data
		} else {
			next.files[path] = data
		}
	}
	return next, nil
}

type bytesCompiler struct{ built *int }

func (bytesCompiler) Compile([]byte, map[string][]byte) (*Plan, error) { return &Plan{}, nil }
func (c bytesCompiler) State(source []byte, files map[string][]byte) (State, error) {
	*c.built++
	return &bytesState{source: source, files: files}, nil
}

// A chain reads a state only for its exact bytes: the entry and every staged
// file.
func TestStateChainComparesEveryByte(t *testing.T) {
	built := 0
	base := &bytesState{source: []byte("a"), files: map[string][]byte{}}
	chain := newStateChain(Options{Path: "/entry", Base: base, Compiler: bytesCompiler{&built}})
	state, err := chain.at([]byte("a"), nil)
	if err != nil || state != base {
		t.Fatal("the base was not used for its own bytes")
	}
	state, _ = chain.at([]byte("b"), nil)
	if string(state.Source()) != "b" || chain.steps != 1 {
		t.Fatal("a changed entry did not move the chain")
	}
	staged := []File{{Path: "/lib.cicada", Before: []byte("x"), After: []byte("y")}}
	state, _ = chain.at([]byte("b"), staged)
	if string(state.Files()["/lib.cicada"]) != "y" || chain.steps != 2 {
		t.Fatal("a staged file did not move the chain")
	}
	if again, _ := chain.at([]byte("b"), staged); again != state {
		t.Fatal("the same bytes moved the chain")
	}
	staged[0].After = []byte("z")
	if state, _ = chain.at([]byte("b"), staged); string(state.Files()["/lib.cicada"]) != "z" {
		t.Fatal("a staged file's new bytes did not move the chain")
	}
	// A copy of the context from before the file was staged: the state holds
	// a file the context does not, so a fresh state is built.
	state, _ = chain.at([]byte("b"), nil)
	if built != 1 || len(state.Files()) != 0 || string(state.Source()) != "b" {
		t.Fatalf("a context with fewer staged files read the chain's state (built %d)", built)
	}
	other := []File{{Path: "/other.cicada", After: []byte("o")}}
	built = 0
	if state, _ = chain.at([]byte("b"), other); built != 1 || state.Files()["/lib.cicada"] != nil {
		t.Fatal("a staged file in place of another read the chain's state")
	}
	chain.rollback()
	if chain.current != base || chain.steps != 0 {
		t.Fatal("rollback did not return to the base")
	}
}
