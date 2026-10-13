package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

const automationScore = "tempo 120\ntrack bass acid { cutoff = 700Hz }\npattern riff acid steps=2 { 1 . }\nscene main {\n  bass = riff // keep binding\n  bass.cutoff = 900.0Hz // keep point comment\n}\nscene hold { bass = keep }\nsong { main hold main }\n"

func TestSceneAutomationPreservesSourceAndValidatesCatalogValues(t *testing.T) {
	source := []byte(strings.ReplaceAll(automationScore, "\n", "\r\n"))
	updated, err := intentSceneSetting(t, source, "main", "bass.cutoff", json.RawMessage(`"1.25kHz"`), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"bass = riff // keep binding\r\n", "bass.cutoff = 1250Hz // keep point comment\r\n", "scene hold { bass = keep }\r\n"} {
		if !bytes.Contains(updated, []byte(text)) {
			t.Fatalf("lost %q: %s", text, updated)
		}
	}
	updated, err = intentSceneSetting(t, updated, "hold", "bass.level", json.RawMessage(`-6.25`), false)
	if err != nil || !bytes.Contains(updated, []byte("bass.level = -6.25dB\r\n")) {
		t.Fatalf("automation precision: %v %s", err, updated)
	}
	p := patternProject(t, updated)
	if got := *p.Scenes[1].Settings[0].Value.Number; got != -6.25 {
		t.Fatalf("gain rounded to %g", got)
	}
	removed, err := intentSceneSetting(t, updated, "main", "bass.cutoff", nil, true)
	if err != nil || !bytes.Contains(removed, []byte("// keep point comment\r\n")) || bytes.Contains(removed, []byte("bass.cutoff =")) {
		t.Fatalf("point removal: %v %s", err, removed)
	}
	patternProject(t, removed)
	if bytes.Count(removed, []byte("\n")) != bytes.Count(removed, []byte("\r\n")) {
		t.Fatal("CRLF changed")
	}
	for _, tc := range []struct{ scene, path, value string }{
		{"main", "bass.cutoff", `50000`},
		{"main", "bass.cutoff", `"oops\ntrack intruder acid {}"`},
		{"main", "bass.level", `"2Hz"`},
		{"main", "bass.insert", `"none"`},
		{"main", "tempo", `19`},
		{"main", "tempo", `301`},
		{"main", "tempo", `"84Hz"`},
		{"main", "global.tempo", `84`},
		{"main", "unknown.pan", `0`},
		{"missing", "bass.pan", `0`},
	} {
		if _, err := intentSceneSetting(t, source, tc.scene, tc.path, json.RawMessage(tc.value), false); err == nil {
			t.Fatalf("accepted unsafe point %+v", tc)
		}
	}
}

func TestSceneAutomationAtomicWriteConflictAndUndo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	source := []byte(automationScore)
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	edit := studioEdit{Revision: studioRevision(source), Action: "automation-set", Scene: "hold", Path: "bass.pan", Value: json.RawMessage(`0.375`)}
	response := studioCall(t, handler, "/api/automation", edit)
	if response.Code != 200 {
		t.Fatalf("save point: %d %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if stale := studioCall(t, handler, "/api/automation", edit); stale.Code != 409 {
		t.Fatalf("stale point: %d", stale.Code)
	}
	edit.Revision, edit.Value = studioRevision(updated), json.RawMessage(`2`)
	if invalid := studioCall(t, handler, "/api/automation", edit); invalid.Code != 422 {
		t.Fatalf("invalid pan: %d", invalid.Code)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(updated, unchanged) {
		t.Fatal("rejected automation write changed source")
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(updated)})
	restored, _ := os.ReadFile(path)
	if undo.Code != 200 || !bytes.Equal(source, restored) {
		t.Fatal("automation Undo lost the exact source")
	}
}

// A scene sets, changes, and removes its tempo through the edit service, and
// each step undoes to the exact earlier bytes.
func TestSceneTempoIsAuthoredThroughTheEditService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	source := []byte(automationScore)
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	read := func() []byte { data, _ := os.ReadFile(path); return data }
	step := func(action, scene, value string, want int) []byte {
		t.Helper()
		before := read()
		edit := studioEdit{Revision: studioRevision(before), Action: action, Scene: scene, Path: "tempo"}
		if value != "" {
			edit.Value = json.RawMessage(value)
		}
		if response := studioCall(t, handler, "/api/automation", edit); response.Code != want {
			t.Fatalf("%s %s %s: %d %s", action, scene, value, response.Code, response.Body.String())
		}
		return before
	}
	step("automation-set", "hold", "84", 200)
	if !bytes.Contains(read(), []byte("scene hold {\n  tempo = 84\n bass = keep }")) {
		t.Fatalf("tempo was not added to the scene: %s", read())
	}
	p := patternProject(t, read())
	if milli, ok := project.SceneTempoMilli(p.Scenes[1]); !ok || milli != 84_000 {
		t.Fatalf("scene tempo = %d, %v", milli, ok)
	}
	afterAdd := read()
	step("automation-set", "hold", "66.5", 200)
	if !bytes.Contains(read(), []byte("tempo = 66.5")) || bytes.Contains(read(), []byte("tempo = 84")) {
		t.Fatalf("tempo was not changed in place: %s", read())
	}
	afterChange := read()
	step("automation-remove", "hold", "", 200)
	if bytes.Contains(read(), []byte("tempo =")) || !bytes.Contains(read(), []byte("bass = keep")) {
		t.Fatalf("tempo was not removed: %s", read())
	}
	step("automation-set", "hold", "301", 422)
	step("automation-remove", "hold", "", 422)
	for _, want := range [][]byte{afterChange, afterAdd, source} {
		// Undo needs the revision of the bytes it reverses.
		undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(read())})
		if undo.Code != 200 {
			t.Fatalf("undo: %d %s", undo.Code, undo.Body.String())
		}
		if got := read(); !bytes.Equal(got, want) {
			t.Fatalf("undo restored %q, want %q", got, want)
		}
	}
}
