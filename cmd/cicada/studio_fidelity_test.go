package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/notation"
)

func TestCommitTailAppendsEditLogAndKeepsHistoryBehaviour(t *testing.T) {
	handler, path := studioTestHandler(t)
	revision := studioRevision([]byte(studioScore))
	if r := studioCall(t, handler, "/api/toggle", studioEdit{Revision: revision, Pattern: "pulse", Step: 0, Author: "tester", Session: "s1"}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	records, _, err := editlog.ReadLog(editlog.Path(path))
	if err != nil || len(records) != 1 || records[0].Parent != revision || records[0].Author != "tester" || records[0].Session != "s1" || records[0].Label != "Grid · pulse step 1 toggled" {
		t.Fatalf("%+v %v", records, err)
	}
	content, _ := os.ReadFile(path)
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(content)}); r.Code != 200 {
		t.Fatalf("undo %d %s", r.Code, r.Body.String())
	}
	records, _, _ = editlog.ReadLog(editlog.Path(path))
	if len(records) != 2 || !strings.HasPrefix(records[1].Label, "Undo · ") || len(records[1].Intents) != 0 {
		t.Fatalf("undo not logged: %+v", records)
	}
}

// assertRoute sends body to route on a fresh studio over fixture and checks
// status, resulting bytes (or unchanged bytes plus error text), and the
// newest history label. Expected bytes are the old writer's output.
func assertRoute(t *testing.T, fixture []byte, route string, body studioEdit, wantStatus int, wantBytes []byte, wantLabel, wantError string) {
	t.Helper()
	path := studioTestPath(t, string(fixture))
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	body.Revision = studioRevision(fixture)
	response := studioCall(t, handler, route, body)
	if response.Code != wantStatus {
		t.Fatalf("%s status %d, want %d: %s", route, response.Code, wantStatus, response.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if wantError != "" {
		var reply struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || reply.Error != wantError {
			t.Fatalf("%s error %q, want %q (%v)", route, reply.Error, wantError, err)
		}
		if !bytes.Equal(got, fixture) {
			t.Fatalf("%s changed the score on an error", route)
		}
		return
	}
	if !bytes.Equal(got, wantBytes) {
		t.Fatalf("%s bytes differ\n got: %q\nwant: %q", route, got, wantBytes)
	}
	edits := studioHistoryEdits(t, handler)
	if len(edits) == 0 || edits[len(edits)-1].Label != wantLabel {
		t.Fatalf("%s history %+v, want label %q", route, edits, wantLabel)
	}
}

func instrumentParameterFixture(t *testing.T) []byte {
	t.Helper()
	source, err := addPresetSource([]byte(presetEditScore), "warm-pad", "my-pad", "keys")
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestRouteParity_InstrumentSetParameter(t *testing.T) {
	fixture := instrumentParameterFixture(t)
	score, ds := notation.Parse(fixture)
	if score == nil || hasDiagnosticErrors(ds) {
		t.Fatal(ds)
	}
	set, err := instrumentParameterSource(fixture, score, "keys", "brightness", "0.5", false)
	if err != nil {
		t.Fatal(err)
	}
	// Literal bytes seen from the old writer on this fixture.
	if want := bytes.Replace(fixture, []byte("track keys my-pad {}"), []byte("track keys my-pad { brightness = 0.5Hz }"), 1); !bytes.Equal(set, want) {
		t.Fatalf("old writer bytes changed: %q", set)
	}
	value, _ := json.Marshal("0.5")
	assertRoute(t, fixture, "/api/instrument", studioEdit{Action: "set-parameter", Track: "keys", NewName: "brightness", Value: value}, 200, set, "Set keys.brightness", "")

	setScore, _ := notation.Parse(set)
	reset, err := instrumentParameterSource(set, setScore, "keys", "brightness", "", true)
	if err != nil {
		t.Fatal(err)
	}
	assertRoute(t, set, "/api/instrument", studioEdit{Action: "reset-parameter", Track: "keys", NewName: "brightness"}, 200, reset, "Use default keys.brightness", "")

	assertRoute(t, fixture, "/api/instrument", studioEdit{Action: "set-parameter", Track: "keys", NewName: "nope", Value: json.RawMessage("1")}, 422, nil, "", `instrument my-pad does not declare parameter "nope"`)
	assertRoute(t, fixture, "/api/instrument", studioEdit{Action: "set-parameter", Track: "keys", NewName: "brightness"}, 422, nil, "", "parameter value must be a number or compatible unit literal")
	assertRoute(t, fixture, "/api/instrument", studioEdit{Action: "set-parameter", Track: "bass", NewName: "brightness", Value: value}, 422, nil, "", `track "bass" has no authored instrument`)
}

func TestFidelityRule1_ReadEndpointsNeverWrite(t *testing.T) {
	handler, path := studioTestHandler(t)
	before, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	for _, route := range []string{"/", "/api/state", "/api/workspace", "/api/files", "/api/revision", "/api/meters", "/api/takes", "/api/capture", "/api/instrument-state", "/api/mixer", "/api/transport", "/api/params", "/api/audio/config", "/api/export", "/api/history", "/api/library", "/api/collaboration/store"} {
		studioCall(t, handler, route, nil)
	}
	after, _ := os.ReadFile(path)
	again, _ := os.Stat(path)
	entries, _ := os.ReadDir(filepath.Dir(path))
	if !bytes.Equal(before, after) || !again.ModTime().Equal(info.ModTime()) || len(entries) != 1 {
		t.Fatalf("reads wrote: equal=%v entries=%d", bytes.Equal(before, after), len(entries))
	}
}

func TestFidelityRule3_SetThenUndoRestoresExactBytes(t *testing.T) {
	fixture := instrumentParameterFixture(t)
	path := studioTestPath(t, string(fixture))
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal("0.5")
	if r := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(fixture), Action: "set-parameter", Track: "keys", NewName: "brightness", Value: value}); r.Code != 200 {
		t.Fatalf("set %d %s", r.Code, r.Body.String())
	}
	changed, _ := os.ReadFile(path)
	if bytes.Equal(changed, fixture) {
		t.Fatal("set changed nothing")
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)}); r.Code != 200 {
		t.Fatalf("undo %d %s", r.Code, r.Body.String())
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, fixture) {
		t.Fatalf("undo did not restore the fixture:\n%s", restored)
	}
}

func TestFidelityRule4_ReadingAndNoOpWritesAddNoHistory(t *testing.T) {
	fixture := instrumentParameterFixture(t)
	path := studioTestPath(t, string(fixture))
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	studioCall(t, handler, "/api/mixer", nil)
	studioCall(t, handler, "/api/params", nil)
	r := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(fixture), Action: "reset-parameter", Track: "keys", NewName: "brightness"})
	var reply map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &reply); err != nil || r.Code != 200 || reply["unchanged"] != true {
		t.Fatalf("no-op reset: %d %s", r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, fixture) {
		t.Fatal("no-op reset changed the score")
	}
	if edits := studioHistoryEdits(t, handler); len(edits) != 0 {
		t.Fatalf("no-op wrote history: %+v", edits)
	}
	if records, _, _ := editlog.ReadLog(editlog.Path(path)); len(records) != 0 {
		t.Fatalf("no-op wrote the edit log: %+v", records)
	}
}

func TestFidelityRule7_InvalidSourceNeverReplacesPlayingScore(t *testing.T) {
	handler, path := studioTestHandler(t)
	original := []byte(studioScore)
	r := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision(original), Source: "title \"x\"\n???"})
	if r.Code != 422 {
		t.Fatalf("invalid source: %d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
		t.Fatal("rejected source changed the file")
	}
	invalid := []byte("title \"x\"\n???")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	state := studioCall(t, handler, "/api/state", nil)
	var reply struct {
		Valid           bool   `json:"valid"`
		Revision        string `json:"revision"`
		PlayingRevision string `json:"playingRevision"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Valid || reply.Revision != studioRevision(invalid) || reply.PlayingRevision != studioRevision(original) {
		t.Fatalf("state after external invalid write: %+v", reply)
	}
}

// instrumentParameterRouteMatchesWriter sends set and reset through
// /api/instrument for a score at path and compares with the old writer, which
// parses with parseScoreForPath.
func instrumentParameterRouteMatchesWriter(t *testing.T, path string, source []byte) {
	t.Helper()
	score, ds, err := parseScoreForPath(path, source)
	if err != nil || score == nil || hasDiagnosticErrors(ds) {
		t.Fatalf("fixture invalid: %v %v", err, ds)
	}
	want, err := instrumentParameterSource(source, score, "keys", "brightness", "0.5", false)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal("0.5")
	r := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(source), Action: "set-parameter", Track: "keys", NewName: "brightness", Value: value})
	if r.Code != 200 {
		t.Fatalf("set: %d %s", r.Code, r.Body.String())
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes differ\n got: %q\nwant: %q", got, want)
	}
	if r := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(got), Action: "reset-parameter", Track: "keys", NewName: "brightness"}); r.Code != 200 {
		t.Fatalf("reset: %d %s", r.Code, r.Body.String())
	}
}

func TestRouteParity_InstrumentLooseScoresAndProjects(t *testing.T) {
	fixture := instrumentParameterFixture(t)
	for _, header := range []string{"cicada 1\n", "cicada 2\n"} {
		t.Run("loose "+strings.TrimSpace(header), func(t *testing.T) {
			source := []byte(strings.Replace(string(fixture), "cicada 1\n", header, 1))
			instrumentParameterRouteMatchesWriter(t, studioTestPath(t, string(source)), source)
		})
	}
	t.Run("cicada.mod project", func(t *testing.T) {
		dir := t.TempDir()
		source := []byte(strings.Replace(string(fixture), "cicada 1\n", "", 1))
		path := filepath.Join(dir, "main.cicada")
		for name, data := range map[string][]byte{"main.cicada": source, "cicada.mod": []byte("project patches\ncicada 2\n")} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		instrumentParameterRouteMatchesWriter(t, path, source)
	})
}

func TestRouteParity_InstrumentEmptyPathErrorText(t *testing.T) {
	fixture := instrumentParameterFixture(t)
	value, _ := json.Marshal("0.5")
	assertRoute(t, fixture, "/api/instrument", studioEdit{Action: "set-parameter", Track: "", NewName: "brightness", Value: value}, 422, nil, "", `track "" has no authored instrument`)
	assertRoute(t, fixture, "/api/instrument", studioEdit{Action: "set-parameter", Track: "keys", NewName: "", Value: value}, 422, nil, "", `instrument my-pad does not declare parameter ""`)
}

func TestUndoAuxiliaryWriteFailureKeepsHistoryErrorText(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "main.cicada")
	manifest := filepath.Join(dir, "cicada.mod")
	source := []byte(strings.Replace(studioMixerScore, "cicada 2\n", "", 1))
	for path, data := range map[string][]byte{score: source, manifest: []byte("project patches\ncicada 1\n")} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := studioHandler(score)
	if err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, handler, "/api/mixer", map[string]any{"revision": studioRevision(source), "path": "bass.level", "value": -3.47, "confirmUpgrade": true})
	if r.Code != 200 {
		t.Fatalf("upgrade save: %d %s", r.Code, r.Body.String())
	}
	if err := os.WriteFile(manifest, []byte("project patches\ncicada 2\n# edited elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(score)
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(content)})
	if undo.Code != 409 || !strings.Contains(undo.Body.String(), "history file write failed: ") || !strings.Contains(undo.Body.String(), "; rollback: ") {
		t.Fatalf("undo error text: %d %s", undo.Code, undo.Body.String())
	}
}

func TestFileSessionsGetSessionIDs(t *testing.T) {
	main := copyStudioProject(t)
	part := filepath.Join(filepath.Dir(main), "parts", "patterns.cicada")
	original, err := os.ReadFile(part)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(main)
	if err != nil {
		t.Fatal(err)
	}
	next := string(original) + "\n// edited in a file session\n"
	r := studioCall(t, handler, "/api/files", studioEdit{Revision: studioRevision(original), File: "parts/patterns.cicada", Source: next})
	if r.Code != 200 {
		t.Fatalf("file save: %d %s", r.Code, r.Body.String())
	}
	records, _, err := editlog.ReadLog(editlog.Path(part))
	if err != nil || len(records) != 1 || records[0].Session == "" {
		t.Fatalf("file session not attributed: %+v %v", records, err)
	}
}
