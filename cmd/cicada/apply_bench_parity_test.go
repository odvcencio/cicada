package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/applycount"
	"m31labs.dev/cicada/internal/benchscore"
	"m31labs.dev/cicada/internal/benchscore/applybench"
)

// The edit benchmark (internal/benchscore/applybench) rebuilds the edit
// service's options from public calls, because editOptions and its compiler
// live in this package. Every hot edit on REF-S and REF-P must give the same
// result through both, compile the same project, and, in a build with the
// cicada_counters tag, do the same work; otherwise the benchmark no longer
// measures what Studio does, and the mirror in applybench must follow the
// service.
func TestApplyBenchOptionsMatchTheService(t *testing.T) {
	isolateState(t)
	for _, name := range []string{benchscore.Small, benchscore.Placement} {
		reference, err := benchscore.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		session, err := applybench.NewSession(t.TempDir(), reference.Source)
		if err != nil {
			t.Fatal(err)
		}
		base, err := applybench.FullCompile(session.Path, session.Source, nil)
		if err != nil {
			t.Fatal(err)
		}
		hot, err := applybench.HotEdits(reference, base)
		if err != nil {
			t.Fatal(err)
		}
		s := &studio{path: session.Path}
		for _, edit := range hot {
			if edit.Intent == nil {
				continue
			}
			env := edits.Envelope{Version: edits.EnvelopeVersion, Revision: edits.Revision(session.Source), Intents: []edits.Intent{edit.Intent}}
			serviceOptions, err := s.editOptions(session.Source, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := applycount.Snapshot()
			want, err := edits.Apply(session.Source, env, serviceOptions)
			serviceWork := applycount.Snapshot().Sub(before)
			if err != nil {
				t.Fatalf("%s %s through the service: %v", name, edit.Name, err)
			}
			benchOptions, compiler, err := session.Options(session.Source, &applybench.Hooks{})
			if err != nil {
				t.Fatal(err)
			}
			before = applycount.Snapshot()
			got, err := edits.Apply(session.Source, env, benchOptions)
			benchWork := applycount.Snapshot().Sub(before)
			if err != nil {
				t.Fatalf("%s %s through the benchmark: %v", name, edit.Name, err)
			}
			if !bytes.Equal(got.Source, want.Source) || got.Label != want.Label || !reflect.DeepEqual(got.Labels, want.Labels) ||
				!reflect.DeepEqual(got.Warnings, want.Warnings) || !reflect.DeepEqual(got.Response, want.Response) || got.Unchanged != want.Unchanged {
				t.Fatalf("%s %s: the benchmark's result differs from the service's", name, edit.Name)
			}
			serviceProject := serviceOptions.Compiler.(*studioCompiler).project
			wantJSON, err := json.Marshal(serviceProject)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, err := json.Marshal(compiler.Last.Project)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotJSON, wantJSON) || !reflect.DeepEqual(compiler.Last.Project, serviceProject) || got.Plan.Revision != want.Plan.Revision {
				t.Fatalf("%s %s: the benchmark compiled another project than the service", name, edit.Name)
			}
			if benchWork != serviceWork {
				t.Fatalf("%s %s: the benchmark did other work than the service:\n%+v\n%+v", name, edit.Name, benchWork, serviceWork)
			}
		}
	}
}
