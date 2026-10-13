package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const studioPlacementScore = `cicada 2
title "Placement edits"
track bass acid {}
pattern pulse { 1 . 5 . }
arrange {
  // Opening stays beside this placement.
  place one bass pulse { at = @1.1.1 length = 4bars } // keep this comment
  place two bass pulse { at = @5.1.1 length = 4bars }
  marker return { at = @5.1.1 }
}
`

func TestPlacementEditsPatchFieldsAtomicallyAndPreserveSource(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		before := []byte(strings.ReplaceAll(studioPlacementScore, "\n", newline))
		edit := studioEdit{Action: "update", Name: "one", Placement: &studioPlaceSettings{At: "@2.1.1", Length: "3bars"}}
		updated, err := intentArrangement(t, before, edit)
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Replace(before, []byte("at = @1.1.1 length = 4bars"), []byte("at = @2.1.1 length = 3bars"), 1)
		if !bytes.Equal(updated, want) {
			t.Fatalf("unrelated source changed:\n%s", updated)
		}
		// Moving first with the old length would overlap 'two'. Only the final
		// simultaneous move and trim is valid.
		path := filepath.Join(t.TempDir(), "main.cicada")
		if err := os.WriteFile(path, updated, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := compileStudioSource(path, updated); err != nil {
			t.Fatalf("atomic move/trim did not compile: %v", err)
		}
		copy, err := intentArrangement(t, updated, studioEdit{Action: "duplicate", Name: "one", NewName: "one-copy", Placement: &studioPlaceSettings{At: "@9.1.1"}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(copy, []byte("place one-copy bass pulse { at = @9.1.1 length = 3bars }")) || !bytes.Contains(copy, []byte("place one bass pulse { at = @2.1.1 length = 3bars } // keep this comment")) {
			t.Fatalf("duplicate moved an attached comment:\n%s", copy)
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(copy, []byte(newline), nil), []byte("\n")) {
			t.Fatal("duplicate changed line endings")
		}
		removed, err := intentArrangement(t, copy, studioEdit{Action: "delete", Name: "one-copy"})
		if err != nil || bytes.Contains(removed, []byte("one-copy")) || !bytes.Contains(removed, []byte("// keep this comment")) {
			t.Fatalf("remove changed another placement: %v", err)
		}
	}
}

func TestPlacementRouteRejectsOverlapsAndStaleWritesAndSupportsUndo(t *testing.T) {
	before := []byte(studioPlacementScore)
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	edit := studioEdit{Revision: studioRevision(before), Action: "update", Name: "one", Placement: &studioPlaceSettings{At: "@2.1.1", Length: "3bars"}}
	response := studioCall(t, handler, "/api/arrange", edit)
	if response.Code != http.StatusOK {
		t.Fatalf("update: %d %s", response.Code, response.Body.String())
	}
	accepted, _ := os.ReadFile(path)
	if bytes.Equal(before, accepted) {
		t.Fatal("placement was not changed")
	}
	if response := studioCall(t, handler, "/api/arrange", edit); response.Code != http.StatusConflict {
		t.Fatalf("stale revision accepted: %d", response.Code)
	}
	edit.Revision = studioRevision(accepted)
	for _, settings := range []*studioPlaceSettings{
		{At: "@3.1.1"}, // overlaps 'two'
		{At: "1ticks"}, // step pattern cannot start between sixteenths
		{Length: "0ticks"},
		{Length: "1/7"}, // cannot represent this exactly at the fixed PPQ
		{Content: "missing"},
		{Track: "bass\nmarker injected { at = @1.1.1 }"},
	} {
		edit.Placement = settings
		response := studioCall(t, handler, "/api/arrange", edit)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid edit %+v: %d %s", settings, response.Code, response.Body.String())
		}
		current, _ := os.ReadFile(path)
		if !bytes.Equal(current, accepted) {
			t.Fatal("rejected edit wrote the file")
		}
	}
	if response := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(accepted)}); response.Code != http.StatusOK {
		t.Fatalf("Undo: %d %s", response.Code, response.Body.String())
	}
	undone, _ := os.ReadFile(path)
	if !bytes.Equal(undone, before) {
		t.Fatal("one Undo did not restore the entire move/trim including comments")
	}
	if response := studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision(undone)}); response.Code != http.StatusOK {
		t.Fatalf("Redo: %d %s", response.Code, response.Body.String())
	}
	redone, _ := os.ReadFile(path)
	if !bytes.Equal(redone, accepted) {
		t.Fatal("Redo changed the accepted bytes")
	}
}

func TestPlacementAppendAndMarkerCommands(t *testing.T) {
	source := []byte(studioPlacementScore)
	for _, edit := range []studioEdit{
		{Action: "append", NewName: "ending", Placement: &studioPlaceSettings{Track: "bass", Content: "pulse", At: "@9.1.1", Length: "320ticks"}},
		{Action: "marker", Name: "return", Placement: &studioPlaceSettings{At: "@9.1.1"}},
		{Action: "append-marker", NewName: "finish", Placement: &studioPlaceSettings{At: "@10.1.1"}},
		{Action: "delete-marker", Name: "return"},
	} {
		var err error
		source, err = intentArrangement(t, source, edit)
		if err != nil {
			t.Fatal(err)
		}
	}
	score, ds := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(ds) || len(score.Arrange.Placements) != 3 || len(score.Arrange.Markers) != 1 || score.Arrange.Markers[0].Name != "finish" || score.Arrange.Placements[2].LengthTicks != 320 {
		t.Fatalf("placement/marker commands: %s\n%v", source, ds)
	}
	for _, edit := range []studioEdit{
		{Action: "append", NewName: "finish", Placement: &studioPlaceSettings{At: "@12.1.1", Track: "bass", Content: "pulse", Length: "1bars"}},
		{Action: "update", Name: "missing", Placement: &studioPlaceSettings{At: "@1.1.1"}},
		{Action: "marker", Name: "finish", Placement: &studioPlaceSettings{Length: "1bars"}},
	} {
		if _, err := intentArrangement(t, source, edit); err == nil {
			t.Fatalf("invalid arrangement edit accepted: %+v", edit)
		}
	}
	for _, name := range []string{"ending", "two"} {
		var err error
		source, err = intentArrangement(t, source, studioEdit{Action: "delete", Name: name})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := intentArrangement(t, source, studioEdit{Action: "delete", Name: "one"}); err == nil {
		t.Fatal("deleted the last placement")
	}
}

func TestPlacementEditsCompileOtherProjectFiles(t *testing.T) {
	root := t.TempDir()
	before := []byte(strings.Replace(studioPlacementScore, "pattern pulse { 1 . 5 . }\n", "", 1))
	parts := []byte("pattern pulse { 1 . 5 . }\n")
	for name, data := range map[string][]byte{
		"cicada.mod":  []byte("project placements\ncicada 2\nentry \"main.cicada\"\nsource \"patterns.cicada\"\n"),
		"main.cicada": before, "patterns.cicada": parts,
	} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := studioHandler(filepath.Join(root, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/arrange", studioEdit{Revision: studioRevision(before), Name: "one", Action: "update", Placement: &studioPlaceSettings{At: "@2.1.1", Length: "3bars"}})
	if response.Code != http.StatusOK {
		t.Fatalf("multi-file placement: %d %s", response.Code, response.Body.String())
	}
	unchanged, _ := os.ReadFile(filepath.Join(root, "patterns.cicada"))
	if !bytes.Equal(unchanged, parts) {
		t.Fatal("placement edit changed the pattern source")
	}
}
