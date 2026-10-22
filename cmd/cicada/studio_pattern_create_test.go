package main

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestStudioCreatePatternAndAssignmentUndoTogether(t *testing.T) {
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
	response := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(read()), Action: "add-preset", Pattern: "warm-pad", NewName: "warm", Track: "keys"})
	if response.Code != http.StatusOK {
		t.Fatalf("add sound: %d %s", response.Code, response.Body)
	}
	before := read()
	response = studioCall(t, handler, "/api/pattern", studioEdit{SourceRevision: studioProjectReceipt(t, s.path), Revision: studioRevision(before), Action: "create", NewName: "glow", Track: "keys", Scene: "main", Length: 16, Starter: "chords"})
	if response.Code != http.StatusOK {
		t.Fatalf("create and assign: %d %s", response.Code, response.Body)
	}
	created := read()
	if !bytes.Contains(created, []byte("pattern glow notes steps=16")) || !bytes.Contains(created, []byte("[1 3 5] - - - - - - - [3 5 7]")) || !bytes.Contains(created, []byte("keys = glow")) || !bytes.Contains(created, []byte("// keep binding comment")) {
		t.Fatal("new pattern or binding is missing, or an existing annotation was lost")
	}
	if response = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(created)}); response.Code != http.StatusOK || !bytes.Equal(read(), before) {
		t.Fatalf("one Undo did not remove creation and binding: %d %s", response.Code, response.Body)
	}
	if response = studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision(before)}); response.Code != http.StatusOK || !bytes.Equal(read(), created) {
		t.Fatalf("Redo did not restore the exact candidate: %d %s", response.Code, response.Body)
	}
	response = studioCall(t, handler, "/api/pattern", studioEdit{SourceRevision: studioProjectReceipt(t, s.path), Revision: studioRevision(before), Action: "create", NewName: "other", Track: "keys", Length: 16})
	if response.Code != http.StatusConflict || !bytes.Equal(read(), created) {
		t.Fatal("stale creation changed the saved score")
	}
	response = studioCall(t, handler, "/api/pattern", studioEdit{SourceRevision: studioProjectReceipt(t, s.path), Revision: studioRevision(created), Action: "create", NewName: "glow", Track: "keys", Length: 16})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "already exists") || !bytes.Equal(read(), created) {
		t.Fatalf("duplicate creation was not refused intact: %d %s", response.Code, response.Body)
	}
	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.shutdown() })
	if !bytes.Equal(reopened.lastGoodSource, created) || len(reopened.lastGoodProject.Patterns[1].Data[0].Notes) != 3 {
		t.Fatal("reopening lost the starter notes or saved score")
	}
}
