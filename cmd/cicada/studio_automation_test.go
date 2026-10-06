package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const automationScore = "tempo 120\ntrack bass acid { cutoff = 700Hz }\npattern riff acid steps=2 { 1 . }\nscene main {\n  bass = riff // keep binding\n  bass.cutoff = 900.0Hz // keep point comment\n}\nscene hold { bass = keep }\nsong { main hold main }\n"

func TestSceneAutomationPreservesSourceAndValidatesCatalogValues(t *testing.T) {
	source := []byte(strings.ReplaceAll(automationScore, "\n", "\r\n"))
	updated, err := sceneAutomationSource(source, "main", "bass.cutoff", json.RawMessage(`"1.25kHz"`), false, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"bass = riff // keep binding\r\n", "bass.cutoff = 1250Hz // keep point comment\r\n", "scene hold { bass = keep }\r\n"} {
		if !bytes.Contains(updated, []byte(text)) {
			t.Fatalf("lost %q: %s", text, updated)
		}
	}
	updated, err = sceneAutomationSource(updated, "hold", "bass.level", json.RawMessage(`-6.25`), false, 1)
	if err != nil || !bytes.Contains(updated, []byte("bass.level = -6.25dB\r\n")) {
		t.Fatalf("automation precision: %v %s", err, updated)
	}
	p := patternProject(t, updated)
	if got := *p.Scenes[1].Settings[0].Value.Number; got != -6.25 {
		t.Fatalf("gain rounded to %g", got)
	}
	removed, err := sceneAutomationSource(updated, "main", "bass.cutoff", nil, true, 1)
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
		{"main", "tempo", `140`},
		{"main", "unknown.pan", `0`},
		{"missing", "bass.pan", `0`},
	} {
		if _, err := sceneAutomationSource(source, tc.scene, tc.path, json.RawMessage(tc.value), false, 1); err == nil {
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
