package edit_test

import (
	"bytes"
	"reflect"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const stateScorePath = "/song/main.cicada"

const stateScore = "title \"State\"\ntempo 120\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { bass=pulse }\nsong { main }\n"

// stateCalls counts what Apply asks of the states a stateCompiler builds.
type stateCalls struct {
	built, scores, withs, rollbacks int
	// plans counts Plan on states With derived; basePlans on built ones.
	plans, basePlans int
}

// stateCompiler is a StateCompiler over project.State on in-memory files,
// counting each call. plain makes it keep no states, for the comparison.
type stateCompiler struct {
	files project.MemoryFiles
	calls *stateCalls
	plain bool
}

func (c stateCompiler) options() project.StateOptions { return project.StateOptions{Files: c.files} }

func (c stateCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	return project.NewState(stateScorePath, source, files, c.options()).Plan()
}

func (c stateCompiler) State(source []byte, files map[string][]byte) (edits.State, error) {
	if c.plain {
		return nil, nil
	}
	c.calls.built++
	return countedState{project.NewState(stateScorePath, source, files, c.options()), c.calls, false}, nil
}

type countedState struct {
	*project.State
	calls   *stateCalls
	derived bool
}

func (s countedState) Score() (*notation.Score, []notation.Diagnostic, error) {
	s.calls.scores++
	return s.State.Score()
}

func (s countedState) Plan() (*edits.Plan, error) {
	if s.derived {
		s.calls.plans++
	} else {
		s.calls.basePlans++
	}
	return s.State.Plan()
}

func (s countedState) With(files map[string][]byte) (edits.State, error) {
	s.calls.withs++
	next, err := s.State.With(files)
	if err != nil {
		return nil, err
	}
	return countedState{next.(*project.State), s.calls, true}, nil
}

func (s countedState) Rollback() (edits.State, error) {
	s.calls.rollbacks++
	previous, err := s.State.Rollback()
	if err != nil {
		return nil, err
	}
	return countedState{previous.(*project.State), s.calls, previous.(*project.State) != nil && s.derived}, nil
}

func stateOptions(compiler stateCompiler) edits.Options {
	return edits.Options{
		Compiler: compiler, Path: stateScorePath,
		ParseProject: func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
			overrides := map[string][]byte{stateScorePath: source}
			for path, data := range files {
				overrides[path] = data
			}
			score, ds, _, err := project.LoadScoreIn(compiler.files, stateScorePath, overrides)
			return score, ds, err
		},
	}
}

func newStateCompiler(plain bool) stateCompiler {
	return stateCompiler{files: project.MemoryFiles{Files: map[string][]byte{stateScorePath: []byte(stateScore)}}, calls: &stateCalls{}, plain: plain}
}

// With a base state, an Apply reads the unchanged score from it however many
// times the intent asks, moves to the state of its result once, and compiles
// that state once; the result is the one Apply gives without states.
func TestApplyReadsTheBaseAndCompilesTheResultOnce(t *testing.T) {
	source := []byte(stateScore)
	intents := []edits.Intent{
		&edits.ToggleStep{Entity: "step:pulse/1", Shared: "definition"},
		&edits.SetPitch{Entity: "step:pulse/0", Pitch: 50, Shared: "definition"},
	}
	for _, intent := range intents {
		t.Run(intent.Kind(), func(t *testing.T) {
			env := edits.Envelope{Version: edits.EnvelopeVersion, Revision: edits.Revision(source), Intents: []edits.Intent{intent}}
			want, err := edits.Apply(source, env, stateOptions(newStateCompiler(true)))
			if err != nil {
				t.Fatal(err)
			}
			if want.State != nil {
				t.Fatal("a compiler that keeps no states produced one")
			}
			compiler := newStateCompiler(false)
			base, _ := compiler.State(source, nil)
			if _, err := base.Plan(); err != nil {
				t.Fatal(err)
			}
			*compiler.calls = stateCalls{}
			opts := stateOptions(compiler)
			opts.Base = base
			got, err := edits.Apply(source, env, opts)
			if err != nil {
				t.Fatal(err)
			}
			calls := *compiler.calls
			if calls.built != 0 || calls.withs != 1 || calls.plans != 1 || calls.rollbacks != 0 {
				t.Fatalf("calls %+v: want no new state, one With and one Plan (the result's)", calls)
			}
			if calls.scores == 0 {
				t.Fatal("the intent did not read the base state's score")
			}
			if got.State == nil || !bytes.Equal(got.State.Source(), got.Source) || len(got.State.Files()) != 0 {
				t.Fatal("the result does not carry the state of its bytes")
			}
			if !bytes.Equal(base.Source(), source) {
				t.Fatal("the base state changed")
			}
			if !bytes.Equal(got.Source, want.Source) || got.Label != want.Label || !reflect.DeepEqual(got.Labels, want.Labels) || got.Plan.Revision != want.Plan.Revision {
				t.Fatalf("with a base state the result differs:\n%s\n%s", got.Source, want.Source)
			}
			// Without a base, Apply builds it once.
			compiler = newStateCompiler(false)
			got, err = edits.Apply(source, env, stateOptions(compiler))
			if err != nil {
				t.Fatal(err)
			}
			if calls := *compiler.calls; calls.built != 1 || calls.withs != 1 {
				t.Fatalf("calls %+v without a base: want one state built and one With", calls)
			}
			if !bytes.Equal(got.Source, want.Source) {
				t.Fatal("without a base the result differs")
			}
		})
	}
}

// A refused candidate rolls the chain back to the base, and Apply returns no
// state.
func TestApplyRollsBackARefusedCandidate(t *testing.T) {
	source := []byte(stateScore)
	compiler := newStateCompiler(false)
	base, _ := compiler.State(source, nil)
	opts := stateOptions(compiler)
	opts.Base = base
	env := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.ReplaceText{Source: "???"}}}
	if _, err := edits.Apply(source, env, opts); err == nil {
		t.Fatal("an invalid candidate was accepted")
	}
	if calls := *compiler.calls; calls.withs != 1 || calls.rollbacks != 1 {
		t.Fatalf("calls %+v: want one With and one Rollback", calls)
	}
	opts.KeepInvalid = true
	result, err := edits.Apply(source, env, opts)
	if err != nil || result.Invalid == nil || result.State != nil {
		t.Fatalf("kept invalid result: %v, invalid %v, state %v", err, result.Invalid, result.State)
	}
	// The same base serves the next edit.
	opts.KeepInvalid = false
	env = edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.ToggleStep{Entity: "step:pulse/1", Shared: "definition"}}}
	result, err = edits.Apply(source, env, opts)
	if err != nil || result.State == nil {
		t.Fatalf("after a refusal: %v", err)
	}
}

// Without a host ParseProject, the context parses the buffer as before: a
// state's project parse is not what it would read.
func TestApplyParsesTheBufferWithoutAHostParser(t *testing.T) {
	source := []byte(stateScore)
	compiler := newStateCompiler(false)
	opts := edits.Options{Compiler: compiler, Path: stateScorePath}
	env := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.ToggleStep{Entity: "step:pulse/1"}}}
	result, err := edits.Apply(source, env, opts)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.calls.scores != 0 || result.State == nil {
		t.Fatalf("calls %+v: the buffer parse read a state", *compiler.calls)
	}
}
