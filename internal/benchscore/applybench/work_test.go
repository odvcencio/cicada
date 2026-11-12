//go:build cicada_counters

package applybench_test

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/applycount"
	"m31labs.dev/cicada/internal/benchscore"
	"m31labs.dev/cicada/internal/benchscore/applybench"
	"m31labs.dev/cicada/project"
)

// instruments counts the instruments a reference declares, each of which a
// compile checks by parsing a small score of its own, and reports whether the
// pattern an EditNotes move edits is a performance.
func instruments(t *testing.T, s *applybench.Session, notes string) (count int64, performance bool) {
	t.Helper()
	score, _, err := project.LoadScore(s.Path, map[string][]byte{s.Path: s.Source})
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range score.Patterns {
		if pattern.Name == notes {
			performance = pattern.Performance != nil
		}
	}
	return int64(len(score.Instruments)), performance
}

// The work of one hot edit, counted (spec studio-local-first revision 1,
// PC2): with the kept state the edit service starts from, one Apply parses
// the score once (its result) and runs notation.Validate, ValidateProject
// and the engine build once, for the one state it compiles, and checks no
// instrument, because none changed. Without a kept state, it parses the
// score at most twice (the source and the result), checks each instrument
// once, and runs each check once per state it compiles (at most two).
//
// One exception, left to PC7 (the performance writer patches lines): an
// EditNotes move on a performance (REF-L's) parses the file once more, by
// itself, to find the declaration it rewrites (edit/performance.go,
// replaceProjectPerformance, notation.ParseDocument).
func TestApplyWorkPerState(t *testing.T) {
	names := []string{benchscore.Small, benchscore.Placement, benchscore.LargeName}
	if testing.Short() {
		names = names[:2]
	}
	for _, name := range names {
		reference, err := benchscore.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		if refusedUnderJS(name) {
			continue
		}
		s, hot := session(t, reference)
		count, performance := instruments(t, s, reference.NotesPattern)
		for _, h := range hot {
			switch h.Name {
			case applybench.ToggleStep, applybench.SetPitch, applybench.MoveNotes:
			default:
				continue
			}
			for _, noBase := range []bool{false, true} {
				m, err := applybench.Measure(s, h, applybench.Config{Runs: 1, Verify: true, NoBase: noBase})
				if err != nil {
					t.Fatalf("%s %s: %v", name, h.Name, err)
				}
				w := m.Work
				// Score parses: the parses that are not instrument checks.
				states, scoreParses, instrumentChecks := int64(1), w.SyntaxParses, int64(0)
				if noBase {
					states, scoreParses, instrumentChecks = 2, w.SyntaxParses-count, count
				}
				writerParses := int64(0)
				if h.Name == applybench.MoveNotes && performance {
					// replaceProjectPerformance's own parse (PC7).
					writerParses = 1
				}
				scoreParses -= writerParses
				switch {
				case scoreParses > states || scoreParses < 1:
					t.Errorf("%s %s (no base %t): %d score parses, want at most %d", name, h.Name, noBase, scoreParses, states)
				case w.SyntaxParses > states+instrumentChecks+writerParses:
					t.Errorf("%s %s (no base %t): %d syntax parses, want at most %d (%d instrument checks)", name, h.Name, noBase, w.SyntaxParses, states+instrumentChecks+writerParses, instrumentChecks)
				case w.NotationValidations > states+instrumentChecks:
					t.Errorf("%s %s (no base %t): notation.Validate ran %d times, want at most %d", name, h.Name, noBase, w.NotationValidations, states+instrumentChecks)
				case w.ProjectValidations > states || w.EngineBuilds > states || w.EditPlans > states:
					t.Errorf("%s %s (no base %t): ValidateProject %d, engine builds %d, edit plans %d; want at most %d each", name, h.Name, noBase, w.ProjectValidations, w.EngineBuilds, w.EditPlans, states)
				case !noBase && (w.ProjectValidations != 1 || w.EngineBuilds != 1 || w.NotationValidations != 1):
					t.Errorf("%s %s: with a kept state ValidateProject %d, engine builds %d, notation.Validate %d; want 1 each", name, h.Name, w.ProjectValidations, w.EngineBuilds, w.NotationValidations)
				}
				t.Logf("%s %s (no base %t): %+v", name, h.Name, noBase, w.Counts)
			}
		}
	}
}

// A batch of 50 intents (cicada apply) moves one state along: each intent
// parses the bytes the one before it wrote, once, and the result is compiled
// once. On the speed plan's example score (examples/songs/anywhere-you-are).
func TestApplyBatchWork(t *testing.T) {
	s := songSession(t)
	score := s.Source
	intents, err := applybench.DrumToggles(s, 50)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := applybench.KeptState(s.Path, score)
	if err != nil {
		t.Fatal(err)
	}
	opts, compiler, err := s.Options(score, &applybench.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	applybench.WithBase(&opts, compiler, kept)
	before := applycount.Snapshot()
	result, err := edit.Apply(score, edit.Envelope{Version: edit.EnvelopeVersion, Revision: edit.Revision(score), Intents: intents}, opts)
	work := applycount.Snapshot().Sub(before)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(result.Source, score) {
		t.Fatal("the batch changed nothing")
	}
	if work.SyntaxParses > 50 || work.ProjectValidations != 1 || work.EngineBuilds != 1 || compiler.Compiles != 1 {
		t.Fatalf("50 intents: %+v, %d compiles; want at most 50 parses and one compile", work, compiler.Compiles)
	}
	t.Logf("50 intents: %+v", work)
}
