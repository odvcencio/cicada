package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestStudioModulationEditsValidateAndUndo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	original := []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern p { 1 . }\nscene main { bass = p }\nsong { main }\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	apply := func(action, value string) *studioEdit {
		before := read()
		receipt, err := project.ReadScoreRevision(path)
		if err != nil {
			t.Fatal(err)
		}
		e := &studioEdit{Revision: studioRevision(before), SourceRevision: receipt, Action: action, Value: json.RawMessage(value)}
		r := studioCall(t, handler, "/api/automation", e)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", action, r.Code, r.Body.String())
		}
		return e
	}
	apply("mod-source-add", `{"sourcekind":"lfo","name":"drift","rate":"1/4","wave":"sine"}`)
	withSource := read()
	apply("mod-route", `{"operation":"add","source":"drift","target":"bass.pan","mode":"add","depth":"0.2"}`)
	withRoute := read()
	if !bytes.Contains(withRoute, []byte("mod drift -> bass.pan depth = 0.2 add")) {
		t.Fatalf("route not saved: %s", withRoute)
	}
	receipt, err := project.ReadScoreRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := studioEdit{Revision: studioRevision(withRoute), SourceRevision: receipt, Action: "mod-route", Value: json.RawMessage(`{"operation":"set","source":"drift","target":"bass.pan","mode":"add","depth":"2Hz"}`)}
	if r := studioCall(t, handler, "/api/automation", bad); r.Code != 422 || !bytes.Equal(read(), withRoute) {
		t.Fatalf("invalid edit: %d %s", r.Code, r.Body.String())
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(withRoute)}); r.Code != 200 || !bytes.Equal(read(), withSource) {
		t.Fatalf("undo: %d %s", r.Code, r.Body.String())
	}
}
