// Package applybench measures what one edit costs: the hot edits of the
// local-first design (spec studio-local-first revision 1, sections B.6, B.7
// and E.2) applied with edit.Apply to the three reference scores of
// internal/benchscore, with the options Studio's edit service builds, timed and
// counted per Apply.
//
// After every measured edit it checks that the state the edit produced equals
// a full compile of the resulting text (FullCompile and Compare). Until the
// edit path keeps a compiled state between edits (PC2 to PC7 of the revision)
// both sides are full compiles and the check passes trivially; it is the check
// those changes must keep passing.
package applybench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"unicode/utf8"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// State is everything derived from one exact set of source bytes that an edit
// hands on: the project sources, the score and its diagnostics, the compiled
// project and the edit plan. It is the "compiled state" of the revision.
type State struct {
	Source      []byte
	Sources     []notation.SourceFile
	Score       *notation.Score
	Diagnostics []notation.Diagnostic
	Project     *project.Project
	Plan        *edit.Plan
}

// FullCompile compiles source from scratch as the score at path, the way
// Studio's edit service compiles a candidate (cmd/cicada/studio.go,
// compileStudioSourceDetailed, then studioCompiler.Compile's edit plan): read
// the project sources with source over the entry, parse, build the project,
// validate it, apply the schedule rules, and build the edit plan from a
// second read of the sources. overrides holds other project files' bytes by
// absolute path; nil is none. It is the specification an edit's state must
// equal. Its error text is the service's.
func FullCompile(path string, source []byte, overrides map[string][]byte) (*State, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("score is not UTF-8")
	}
	files := make(map[string][]byte, len(overrides)+1)
	for name, data := range overrides {
		files[name] = data
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	files[absolute] = source
	sources, err := project.ReadSources(path, files)
	if err != nil {
		return nil, err
	}
	state := &State{Source: bytes.Clone(source), Sources: sources.Files}
	score, diagnostics := sources.Parse()
	state.Score, state.Diagnostics = score, diagnostics
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
		}
	}
	p, extra := project.FromScore(score)
	state.Diagnostics = append(state.Diagnostics, extra...)
	for _, d := range extra {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Text())
		}
	}
	if p == nil {
		return nil, fmt.Errorf("score did not compile")
	}
	if err := project.ValidateProject(p); err != nil {
		return nil, err
	}
	if err := schedule.Validate(p, sources.Root, 48000, 128, schedule.Options{}); err != nil {
		return nil, err
	}
	state.Project = p
	// The service reads the sources again for the plan's authored names.
	named, err := project.ReadSources(path, files)
	if err != nil {
		return nil, err
	}
	state.Plan = project.EditPlan(p, source, named.Files...)
	return state, nil
}

// Compare reports the first way produced differs from fresh, a full compile
// of the same bytes (section B.6 of the revision): the sources, the score with
// its positions, the diagnostics in content and order, the project (its
// canonical bytes and every field, unexported ones included), the edit plan's
// data and the answers of its functions on fixed inputs, and the revision.
// The syntax trees join the comparison when the state keeps them (PC3).
func Compare(produced, fresh *State) error {
	if produced == nil || fresh == nil {
		return fmt.Errorf("a state is missing (produced %t, full compile %t)", produced != nil, fresh != nil)
	}
	if !bytes.Equal(produced.Source, fresh.Source) {
		return fmt.Errorf("source: the state holds %d bytes, the edit returned %d", len(produced.Source), len(fresh.Source))
	}
	if !reflect.DeepEqual(produced.Sources, fresh.Sources) {
		return fmt.Errorf("sources differ: %d files against %d", len(produced.Sources), len(fresh.Sources))
	}
	if !reflect.DeepEqual(produced.Score, fresh.Score) {
		return fmt.Errorf("the notation.Score differs from a full parse")
	}
	if !reflect.DeepEqual(produced.Diagnostics, fresh.Diagnostics) {
		return fmt.Errorf("diagnostics differ: %d against %d", len(produced.Diagnostics), len(fresh.Diagnostics))
	}
	producedJSON, err := json.Marshal(produced.Project)
	if err != nil {
		return fmt.Errorf("project: %w", err)
	}
	freshJSON, err := json.Marshal(fresh.Project)
	if err != nil {
		return fmt.Errorf("project of the full compile: %w", err)
	}
	if !bytes.Equal(producedJSON, freshJSON) {
		return fmt.Errorf("the project's canonical bytes differ (%d against %d)", len(producedJSON), len(freshJSON))
	}
	if !reflect.DeepEqual(produced.Project, fresh.Project) {
		return fmt.Errorf("the project differs from a full compile in a field its canonical bytes omit")
	}
	if err := comparePlans(produced.Plan, fresh.Plan, fresh.Project); err != nil {
		return fmt.Errorf("edit plan: %w", err)
	}
	if want := edit.Revision(fresh.Source); produced.Plan.Revision != want {
		return fmt.Errorf("revision %s; the bytes hash to %s", produced.Plan.Revision, want)
	}
	return nil
}

// comparePlans compares two plans' data, then their functions by their answers
// (CopySource of every pattern, CopyForTrack of the first pattern on the first
// track, ResolveParam of every parameter address of the project).
func comparePlans(produced, fresh *edit.Plan, p *project.Project) error {
	if produced == nil || fresh == nil {
		return fmt.Errorf("a plan is missing (produced %t, full compile %t)", produced != nil, fresh != nil)
	}
	if !reflect.DeepEqual(planData(produced), planData(fresh)) {
		return fmt.Errorf("the plans' data differ")
	}
	type answer struct {
		Text string
		Err  string
	}
	call := func(text string, err error) answer {
		a := answer{Text: text}
		if err != nil {
			a.Err = err.Error()
		}
		return a
	}
	for i := range fresh.Patterns {
		a, b := produced.Patterns[i], fresh.Patterns[i]
		if (a.CopySource == nil) != (b.CopySource == nil) || (a.CopyForTrack == nil) != (b.CopyForTrack == nil) {
			return fmt.Errorf("pattern %s: a copy function is missing on one side", b.ID)
		}
		if b.CopySource != nil && call(a.CopySource("benchscore-copy")) != call(b.CopySource("benchscore-copy")) {
			return fmt.Errorf("pattern %s: CopySource answers differ", b.ID)
		}
		// CopyForTrack parses the project, so one pattern is enough.
		if i == 0 && b.CopyForTrack != nil && len(fresh.Tracks) > 0 {
			track := fresh.Tracks[0].ID
			if call(a.CopyForTrack("benchscore-copy", track)) != call(b.CopyForTrack("benchscore-copy", track)) {
				return fmt.Errorf("pattern %s: CopyForTrack answers differ", b.ID)
			}
		}
	}
	if (produced.ResolveParam == nil) != (fresh.ResolveParam == nil) {
		return fmt.Errorf("ResolveParam is missing on one side")
	}
	if fresh.ResolveParam != nil {
		paths := []string{"no.such.parameter"}
		for _, address := range project.ParamAddresses(p) {
			paths = append(paths, address.Address)
		}
		for _, path := range paths {
			x, xerr := produced.ResolveParam(path)
			y, yerr := fresh.ResolveParam(path)
			if !reflect.DeepEqual(x, y) || fmt.Sprint(xerr) != fmt.Sprint(yerr) {
				return fmt.Errorf("ResolveParam(%q) answers differ", path)
			}
		}
	}
	return nil
}

// planData returns a copy of plan without its function fields, which
// reflect.DeepEqual cannot compare.
func planData(plan *edit.Plan) edit.Plan {
	data := *plan
	data.ResolveParam = nil
	data.Patterns = append([]edit.Pattern(nil), plan.Patterns...)
	for i := range data.Patterns {
		data.Patterns[i].CopySource, data.Patterns[i].CopyForTrack = nil, nil
	}
	return data
}

// StateOf returns what a compiled project.State holds, in the shape Compare
// reads. The slices and pointers are the state's own, not copies.
func StateOf(compiled *project.State) (*State, error) {
	p, sources, diagnostics, err := compiled.Detailed(false)
	if err != nil {
		return nil, err
	}
	score, _, err := compiled.Score()
	if err != nil {
		return nil, err
	}
	plan, err := compiled.Plan()
	if err != nil {
		return nil, err
	}
	return &State{Source: compiled.Source(), Sources: sources.Files, Score: score, Diagnostics: diagnostics, Project: p, Plan: plan}, nil
}
