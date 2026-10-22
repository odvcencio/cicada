package main

import (
	"bytes"
	"net/http"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestStudioChordPlacementUndoAndVoicingEdits(t *testing.T) {
	t.Setenv("CICADA_AUDIO_SETTINGS_DIR", t.TempDir())
	s := newTakeStudio(t, t.TempDir())
	handler := s.routes()
	read := func() []byte {
		t.Helper()
		data, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	call := func(in studioEdit) {
		t.Helper()
		in.Revision = studioRevision(read())
		r := studioCall(t, handler, "/api/pattern", in)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", in.Action, r.Code, r.Body)
		}
	}
	r := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(read()), Action: "add-preset", Pattern: "warm-pad", NewName: "warm", Track: "keys"})
	if r.Code != 200 {
		t.Fatalf("add sound: %d %s", r.Code, r.Body)
	}
	call(studioEdit{Action: "create", NewName: "harmony", Track: "keys", Scene: "main", Length: 16})
	before := read()
	call(studioEdit{Action: "chord", Pattern: "harmony", Step: 0, Chord: &edit.SetChord{Notes: []int{57, 60, 64}, Hold: 16}})
	created := read()
	assertNotes := func(name string, want []int) {
		t.Helper()
		score, ds := notation.Parse(read())
		if score == nil {
			t.Fatalf("parse: %+v", ds)
		}
		p, ds := project.FromScore(score)
		if p == nil {
			t.Fatalf("compile: %+v", ds)
		}
		for _, pattern := range p.Patterns {
			if pattern.ID == name {
				if !reflect.DeepEqual(pattern.Data[0].Notes, want) || !pattern.Data[15].Tie {
					t.Fatalf("voicing or hold lost: %+v", pattern.Data)
				}
				return
			}
		}
		t.Fatalf("pattern %s missing", name)
	}
	assertNotes("harmony", []int{57, 60, 64})
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(created)}); r.Code != 200 || !bytes.Equal(read(), before) {
		t.Fatal("chord and holds did not undo together")
	}
	if r := studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision(before)}); r.Code != 200 || !bytes.Equal(read(), created) {
		t.Fatal("redo did not restore the exact voicing")
	}
	call(studioEdit{Action: "duplicate", Pattern: "harmony", NewName: "variation"})
	assertNotes("variation", []int{57, 60, 64})
	call(studioEdit{Action: "step", Pattern: "harmony", Step: 0, NoteEdit: &studioStepEdit{Mode: "note", Pitch: 59, Ratchet: 1, Chance: 100}})
	assertNotes("harmony", []int{59, 62, 66})
	last := read()
	r = studioCall(t, handler, "/api/pattern", studioEdit{SourceRevision: studioProjectReceipt(t, s.path), Revision: studioRevision(last), Action: "chord", Pattern: "harmony", Step: 15, Chord: &edit.SetChord{Notes: []int{57, 60, 64}, Hold: 2}})
	if r.Code != http.StatusUnprocessableEntity || !bytes.Equal(last, read()) {
		t.Fatal("out-of-range hold changed the project")
	}
	r = studioCall(t, handler, "/api/pattern", studioEdit{SourceRevision: studioProjectReceipt(t, s.path), Revision: studioRevision(before), Action: "chord", Pattern: "harmony", Step: 0, Chord: &edit.SetChord{Notes: []int{57, 60, 64}, Hold: 16}})
	if r.Code != http.StatusConflict || !bytes.Equal(last, read()) {
		t.Fatal("stale chord changed the project")
	}
}
