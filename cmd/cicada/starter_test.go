package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The manual and README point new readers at examples/first-song. It must be
// exactly what `cicada new first-song` writes, so a reader who opens the example
// sees the project the command creates.
func TestStarterMatchesFirstSongExample(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("first-song", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cicada.mod", "main.cicada"} {
		written, err := os.ReadFile(filepath.Join(root, "first-song", name))
		if err != nil {
			t.Fatal(err)
		}
		checkedIn, err := os.ReadFile(filepath.Join(repositoryRoot(), "examples", "first-song", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(written, checkedIn) {
			t.Errorf("examples/first-song/%s differs from what `cicada new first-song` writes; regenerate it from a new project\nwritten:\n%s\nchecked in:\n%s", name, written, checkedIn)
		}
	}
}

// In Studio a take can be recorded into the starter's bass, whose pattern is
// single notes. The chords track holds chords, so a take cannot be snapped to
// its step grid: the refusal names the cause and leaves the score untouched.
func TestStarterBassRecordsATakeAndChordsRefuseOneAtomically(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("song", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "song", "main.cicada")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	// C3 on the second step: unlike A2 on the first, it changes the pulse pattern.
	take := []studioTakeNote{{Tick: 240, EndTick: 300, Note: 48, Velocity: 90}}
	record := func(track, pattern string) *httptest.ResponseRecorder {
		return studioCall(t, handler, "/api/record", studioEdit{Revision: studioRevision(before), SourceRevision: studioProjectReceipt(t, path), Track: track, Pattern: pattern, Take: take})
	}
	refused := record("chords", "progression")
	after, err := os.ReadFile(path)
	if err != nil || refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), "plays chords or note gestures") || !bytes.Equal(after, before) {
		t.Fatalf("a take into the chords track was not refused atomically: %d %s %v", refused.Code, refused.Body.String(), err)
	}
	if strings.Contains(refused.Body.String(), "acid") {
		t.Fatalf("the refusal names the acid voice: %s", refused.Body.String())
	}
	accepted := record("bass", "pulse")
	after, err = os.ReadFile(path)
	if err != nil || accepted.Code != http.StatusOK || bytes.Equal(after, before) || !bytes.Contains(after, []byte("pattern pulse")) {
		t.Fatalf("a single-note take into the bass track was not accepted: %d %s %v", accepted.Code, accepted.Body.String(), err)
	}
}
