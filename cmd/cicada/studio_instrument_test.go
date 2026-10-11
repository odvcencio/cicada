package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const presetEditScore = "cicada 1\ntitle \"Keep my title\"\n// authored track\ntrack bass acid { cutoff = 723.25Hz } // preserve this\npattern p acid { 1 . 5 . }\n// scene annotation\nscene main { bass=p }\nsong { main*2 }\n"

func TestPresetCreationRejectsStaleWritesAndUndoesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.cicada")
	before := []byte(presetEditScore)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	edit := studioEdit{Revision: studioRevision(before), Action: "add-preset", Pattern: "poly-brass", NewName: "brass", Track: "keys"}
	response := studioCall(t, handler, "/api/instrument", edit)
	if response.Code != 200 {
		t.Fatalf("preset creation: %d %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if !bytes.Contains(updated, []byte("instrument brass")) || !bytes.Contains(updated, []byte("track keys brass")) {
		t.Fatal("creation did not persist both declarations")
	}
	edit.NewName, edit.Track = "next", "next"
	if stale := studioCall(t, handler, "/api/instrument", edit); stale.Code != 409 {
		t.Fatalf("stale preset: %d", stale.Code)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(updated, unchanged) {
		t.Fatal("stale edit wrote source")
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(updated)})
	restored, _ := os.ReadFile(path)
	if undo.Code != 200 || !bytes.Equal(restored, before) {
		t.Fatal("one Undo must restore instrument and track creation")
	}
}

func TestPresetCreationHonorsWholeProjectVoiceBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.cicada")
	source := []byte(presetEditScore)
	for _, name := range []string{"pad-a", "pad-b", "pad-c"} {
		var err error
		source, err = addPresetSource(source, "warm-pad", name, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(source), Action: "add-preset", Pattern: "warm-pad", NewName: "over-budget", Track: "over-budget"})
	if response.Code != 422 {
		t.Fatalf("unbounded preset: %d %s", response.Code, response.Body.String())
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, source) {
		t.Fatal("rejected voice budget changed disk")
	}
}

func TestPresetCreationRespectsInheritedEdition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	before := []byte("title \"Manifest edition\"\ntrack input audio {}\npattern seed-notes notes { c4 . }\nscene main { input=off }\nsong { main }\n")
	for name, data := range map[string][]byte{"main.cicada": before, "cicada.mod": []byte("project patches\ncicada 2\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(before), Action: "add-preset", Pattern: "warm-pad", NewName: "pad", Track: "keys"})
	if response.Code != 200 {
		t.Fatalf("inherited edition preset: %d %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if !bytes.Contains(updated, []byte("track input audio {}")) || bytes.Contains(updated, []byte("cicada 2")) {
		t.Fatal("preset creation changed inherited edition source")
	}
}

func TestInstrumentParameterEditsPreserveSourceAndResetDefaults(t *testing.T) {
	source := []byte(strings.ReplaceAll(`cicada 1
instrument lead-voice {
  param brightness = 720Hz
  param attack = 3ms
  param amount = 0.3
  voice mono {
    let amp = adsr(gate, attack, 80ms, 0.7, 120ms)
    out = svf(sine(pitch), brightness, amount) * amp * 0.1
  }
}
track keys lead-voice {
  brightness = 1.2kHz // authored brightness
  attack = // keep this annotation
    5ms
}
pattern p notes { c4 . }
scene main { keys=p }
song { main }
`, "\n", "\r\n"))
	parse := func(data []byte) *notation.Score {
		t.Helper()
		score, ds := notation.Parse(data)
		if score == nil || hasDiagnosticErrors(ds) {
			t.Fatalf("parameter score: %+v", ds)
		}
		return score
	}
	updated, err := instrumentParameterSource(source, parse(source), "keys", "brightness", "1250.125", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("brightness = 1250.125Hz // authored brightness\r\n")) || !bytes.Contains(updated, []byte("param brightness = 720Hz\r\n")) {
		t.Fatalf("parameter edit changed shared graph defaults or comments: %s", updated)
	}
	updated, err = instrumentParameterSource(updated, parse(updated), "keys", "amount", "0.175", false)
	if err != nil || !bytes.Contains(updated, []byte("amount = 0.175")) {
		t.Fatalf("new override: %v", err)
	}
	reset, err := instrumentParameterSource(updated, parse(updated), "keys", "brightness", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, param := range parse(reset).Tracks[0].Params {
		if param.Name == "brightness" {
			t.Fatal("reset left a brightness override")
		}
	}
	if !bytes.Contains(reset, []byte("// authored brightness\r\n")) || bytes.Contains(bytes.ReplaceAll(reset, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("reset changed comments or newline convention")
	}
	if _, err := instrumentParameterSource(reset, parse(reset), "keys", "attack", "", true); err == nil || !strings.Contains(err.Error(), "preserve the annotation") {
		t.Fatalf("internally commented override needs explicit editing: %v", err)
	}
}

func TestInstrumentParameterWritesCheckRevisionUnitsAndUndo(t *testing.T) {
	source, err := addPresetSource([]byte(presetEditScore), "warm-pad", "pad", "keys")
	if err != nil {
		t.Fatal(err)
	}
	path := studioTestPath(t, string(source))
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal("1.2345kHz")
	edit := studioEdit{Revision: studioRevision(source), Action: "set-parameter", Track: "keys", NewName: "brightness", Value: value}
	if response := studioCall(t, handler, "/api/instrument", edit); response.Code != 200 {
		t.Fatalf("set instrument parameter: %d %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if !bytes.Contains(updated, []byte("brightness = 1.2345kHz")) {
		t.Fatal("parameter precision or units were changed")
	}
	if stale := studioCall(t, handler, "/api/instrument", edit); stale.Code != 409 {
		t.Fatalf("stale parameter: %d", stale.Code)
	}
	edit.Revision = studioRevision(updated)
	for _, bad := range []string{"10ms", "NaNHz", "Infinity"} {
		edit.Value, _ = json.Marshal(bad)
		if response := studioCall(t, handler, "/api/instrument", edit); response.Code != 422 {
			t.Fatalf("invalid parameter %s: %d", bad, response.Code)
		}
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(updated, unchanged) {
		t.Fatal("invalid instrument setting changed disk")
	}
	edit.Action, edit.Value = "reset-parameter", nil
	if response := studioCall(t, handler, "/api/instrument", edit); response.Code != 200 {
		t.Fatalf("reset parameter: %d %s", response.Code, response.Body.String())
	}
	reset, _ := os.ReadFile(path)
	if bytes.Contains(reset, []byte("brightness = 1.2345kHz")) {
		t.Fatalf("reset did not restore graph default: %s", reset)
	}
	if undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(reset)}); undo.Code != 200 {
		t.Fatalf("undo setting: %d", undo.Code)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, updated) {
		t.Fatal("Undo did not restore the exact overridden source")
	}
	edit.Action, edit.Revision, edit.Value = "set-parameter", studioRevision(restored), json.RawMessage("1275.125")
	if response := studioCall(t, handler, "/api/instrument", edit); response.Code != 200 {
		t.Fatalf("numeric JSON parameter: %d %s", response.Code, response.Body.String())
	}
	numeric, _ := os.ReadFile(path)
	if !bytes.Contains(numeric, []byte("brightness = 1275.125Hz")) {
		t.Fatal("numeric JSON lost precision or declared units")
	}
}
