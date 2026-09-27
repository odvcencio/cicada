package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const studioSongScore = "title \"Song lane\"\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene dusk { bass=pulse }\nscene chorus { bass=pulse }\nsong {\n  dusk*2\n  chorus\n  dusk*3\n}\n"

func TestSongEditsPreserveSourceOutsideEntrySpans(t *testing.T) {
	before := []byte(studioSongScore)
	moved, err := editedSongSource(before, "move", 0, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(moved, []byte("song {\n  chorus\n  dusk*3\n  dusk*2\n}")) {
		t.Fatalf("song was not moved: %s", moved)
	}
	if !bytes.Equal(before[:bytes.Index(before, []byte("song {"))], moved[:bytes.Index(moved, []byte("song {"))]) {
		t.Fatal("other declarations changed")
	}
	changed, err := editedSongSource(moved, "bars", 0, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(changed, []byte("song {\n  chorus*8\n  dusk*3")) {
		t.Fatalf("implicit bar count was not added: %s", changed)
	}
	changed, err = editedSongSource(changed, "bars", 1, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(changed, []byte("dusk*5")) {
		t.Fatalf("explicit bar count was not changed: %s", changed)
	}
}

func TestSongReorderRefusesAmbiguousComments(t *testing.T) {
	source := strings.Replace(studioSongScore, "  chorus\n", "  // cue chorus\n  chorus\n", 1)
	if _, err := editedSongSource([]byte(source), "move", 0, 1, 0); err == nil || !strings.Contains(err.Error(), "comments") {
		t.Fatalf("comment was reassigned: %v", err)
	}
	if _, err := editedSongSource([]byte(source), "bars", 1, 0, 4); err != nil {
		t.Fatalf("local bar edit should preserve comment: %v", err)
	}
}

func TestSongEditsPreserveCRLFAndMultiplierSpacing(t *testing.T) {
	source := strings.Replace(studioSongScore, "dusk*2", "dusk * 2", 1)
	source = strings.ReplaceAll(source, "\n", "\r\n")
	updated, err := editedSongSource([]byte(source), "bars", 0, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("dusk * 7\r\n")) {
		t.Fatalf("multiplier spacing changed: %s", updated)
	}
	if bytes.Contains(bytes.ReplaceAll(updated, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("line endings changed")
	}
	moved, err := editedSongSource(updated, "move", 0, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(moved, []byte("chorus\r\n  dusk*3\r\n  dusk * 7")) {
		t.Fatalf("CRLF song reorder changed spelling: %s", moved)
	}
}

func TestStudioSongRouteUpdatesFileAndRejectsStaleRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(studioSongScore), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	page := studioCall(t, handler, "/", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `class="song timeline"`) || !strings.Contains(page.Body.String(), `class="song-grip"`) || !strings.Contains(page.Body.String(), `class="song-resize"`) || !strings.Contains(page.Body.String(), `data-index="2" data-bars="3"`) {
		t.Fatalf("song lane controls missing: %d", page.Code)
	}
	revision := studioRevision([]byte(studioSongScore))
	response := studioCall(t, handler, "/api/song", studioEdit{Revision: revision, Action: "move", Index: 0, Target: 2})
	if response.Code != http.StatusOK {
		t.Fatalf("move: %d %s", response.Code, response.Body.String())
	}
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(content, []byte("song {\n  chorus\n  dusk*3\n  dusk*2\n}")) {
		t.Fatalf("song source not moved: %s %v", content, err)
	}
	stale := studioCall(t, handler, "/api/song", studioEdit{Revision: revision, Action: "bars", Index: 0, Bars: 8})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale song edit accepted: %d", stale.Code)
	}
	updated := studioCall(t, handler, "/api/song", studioEdit{Revision: studioRevision(content), Action: "bars", Index: 0, Bars: 8})
	if updated.Code != http.StatusOK {
		t.Fatalf("bars: %d %s", updated.Code, updated.Body.String())
	}
}
