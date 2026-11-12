package project_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/benchscore/applybench"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var stateOptions = project.StateOptions{Rules: schedule.StateRules}

// exampleScores lists every score under examples/, with its bytes.
func exampleScores(t *testing.T) map[string][]byte {
	t.Helper()
	root, err := filepath.Abs("../examples")
	if err != nil {
		t.Fatal(err)
	}
	scores := map[string][]byte{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".cicada" {
			return err
		}
		scores[path], err = os.ReadFile(path)
		return err
	})
	if err != nil || len(scores) < 100 {
		t.Fatalf("examples: %d scores, %v", len(scores), err)
	}
	return scores
}

// A State is a full compile (section B.6 of the local-first revision, items 2
// to 4): on every example score its parse, diagnostics, project and edit
// plan equal those of the service's full compile, which parses each file
// three times and validates the project four or five times; a score that
// does not compile gives the same error.
func TestStateEqualsAFullCompile(t *testing.T) {
	for path, source := range exampleScores(t) {
		fresh, freshErr := applybench.FullCompile(path, source, nil)
		state := project.NewState(path, source, nil, stateOptions)
		_, err := state.Plan()
		if (err == nil) != (freshErr == nil) || err != nil && err.Error() != freshErr.Error() {
			t.Fatalf("%s: state error %v, full compile %v", path, err, freshErr)
		}
		if err != nil {
			continue
		}
		produced, err := applybench.StateOf(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := applybench.Compare(produced, fresh); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		// The same bytes through With, from another score's state.
		other := project.NewState(path, []byte("title \"other\"\n"), nil, stateOptions)
		next, _ := other.With(map[string][]byte{path: source})
		if produced, err = applybench.StateOf(next.(*project.State)); err != nil {
			t.Fatalf("%s through With: %v", path, err)
		}
		if err := applybench.Compare(produced, fresh); err != nil {
			t.Fatalf("%s through With: %v", path, err)
		}
	}
}

// studioDetailed is cmd/cicada's compileStudioSourceDetailed as it was before
// it compiled through a state, the reference Detailed must equal.
func studioDetailed(path string, source []byte, fileDiagnostics bool) (*project.Project, *project.Sources, []notation.Diagnostic, error) {
	if !utf8.Valid(source) {
		return nil, nil, nil, fmt.Errorf("score is not UTF-8")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, nil, err
	}
	sources, err := project.ReadSources(path, map[string][]byte{absolute: source})
	if err != nil {
		return nil, nil, nil, err
	}
	score, diagnostics := sources.Parse()
	for _, d := range diagnostics {
		if d.Severity == "error" {
			if fileDiagnostics && score != nil {
				_, extra := project.FromScore(score)
				diagnostics = append(diagnostics, extra...)
			}
			return nil, sources, diagnostics, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
		}
	}
	p, extra := project.FromScore(score)
	diagnostics = append(diagnostics, extra...)
	for _, d := range extra {
		if d.Severity == "error" {
			return nil, sources, diagnostics, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
		}
	}
	if p == nil {
		return nil, sources, diagnostics, fmt.Errorf("score did not compile")
	}
	if err := project.ValidateProject(p); err != nil {
		return nil, sources, diagnostics, err
	}
	if err := schedule.Validate(p, sources.Root, 48000, 128, schedule.Options{}); err != nil {
		return nil, sources, diagnostics, err
	}
	return p, sources, diagnostics, nil
}

// Detailed returns what Studio's compile returned before it compiled through
// a state (studioDetailed), with and without file diagnostics: for a score
// that compiles, one whose parse fails, one whose references fail, one that
// FromScore refuses and one that is not UTF-8, and for every example score.
func TestStateDetailedKeepsStudiosResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	cases := map[string][]byte{
		"valid":     []byte("title \"T\"\ntrack bass acid {}\npattern p acid steps=4 { 1 . . . }\nscene s { bass=p }\nsong { s }\n"),
		"syntax":    []byte("track {\n"),
		"reference": []byte("title \"T\"\ntrack bass acid {}\npattern p acid steps=4 { 1 . . . }\nscene s { bass=missing }\nsong { s }\n"),
		"compile":   []byte("title \"" + strings.Repeat("x", 130) + "\"\ntrack bass acid {}\npattern p acid steps=4 { 1 . . . }\nscene s { bass=p }\nsong { s }\n"),
		"utf8":      {0xff, 0xfe},
	}
	check := func(name, path string, source []byte) {
		t.Helper()
		state := project.NewState(path, source, nil, stateOptions)
		for _, fileDiagnostics := range []bool{false, true} {
			p, sources, diagnostics, err := state.Detailed(fileDiagnostics)
			wantP, wantSources, wantDiagnostics, wantErr := studioDetailed(path, source, fileDiagnostics)
			if errorText(err) != errorText(wantErr) || !reflect.DeepEqual(p, wantP) || !reflect.DeepEqual(diagnostics, wantDiagnostics) ||
				(sources == nil) != (wantSources == nil) || sources != nil && !reflect.DeepEqual(sources.Files, wantSources.Files) {
				t.Fatalf("%s (file diagnostics %t): error %v, want %v; %d diagnostics, want %d", name, fileDiagnostics, err, wantErr, len(diagnostics), len(wantDiagnostics))
			}
		}
	}
	for name, source := range cases {
		if err := os.WriteFile(path, source, 0o600); err != nil {
			t.Fatal(err)
		}
		check(name, path, source)
	}
	for name, source := range exampleScores(t) {
		check(name, name, source)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A kept state follows its bytes: SameSources fails once a file changes,
// SelfContained only for a score that reads no audio or library pins, Kept
// drops the staged files and the history, and With leaves the receiver as it
// was.
func TestStateKeepsExactlyItsBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	source := []byte("title \"T\"\ntrack bass acid {}\npattern p acid steps=4 { 1 . . . }\nscene s { bass=p }\nsong { s }\n")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(data []byte) *project.Sources {
		sources, err := project.ReadSources(path, map[string][]byte{path: data})
		if err != nil {
			t.Fatal(err)
		}
		return sources
	}
	state := project.NewStateFromSources(path, source, read(source), stateOptions)
	if state == nil || !state.SameSources(read(source)) || !state.SelfContained() {
		t.Fatal("a state does not hold the sources it was built on")
	}
	if project.NewStateFromSources(path, []byte("other"), read(source), stateOptions) != nil {
		t.Fatal("a state was built on sources that hold other bytes")
	}
	changed := bytes.Replace(source, []byte("1 . . ."), []byte("1 . 5 ."), 1)
	if state.SameSources(read(changed)) {
		t.Fatal("a changed score matched the kept state")
	}
	if _, err := state.Plan(); err != nil {
		t.Fatal(err)
	}
	next, err := state.With(map[string][]byte{path: changed, filepath.Join(dir, "other.cicada"): []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	derived := next.(*project.State)
	if !bytes.Equal(state.Source(), source) || len(state.Files()) != 0 || !bytes.Equal(derived.Source(), changed) || len(derived.Files()) != 1 {
		t.Fatal("With changed its receiver or lost a file")
	}
	if back, err := derived.Rollback(); err != nil || back != state {
		t.Fatal("Rollback did not return the receiver of With")
	}
	kept := derived.Kept()
	if len(kept.Files()) != 0 || !bytes.Equal(kept.Source(), changed) {
		t.Fatal("Kept did not drop the staged files")
	}
	if _, err := kept.Rollback(); err == nil {
		t.Fatal("a kept state rolled back")
	}
	sampled := []byte("title \"T\"\nsampler pads { path = \"pads\" }\n")
	if err := os.WriteFile(path, sampled, 0o600); err != nil {
		t.Fatal(err)
	}
	if project.NewState(path, sampled, nil, stateOptions).SelfContained() {
		t.Fatal("a score with a sampler counts as reading nothing but its sources")
	}
}
