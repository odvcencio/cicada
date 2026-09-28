package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func studioHistoryState(t *testing.T, handler http.Handler) studioHistorySnapshot {
	t.Helper()
	response := studioCall(t, handler, "/api/history", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("history status: %d %s", response.Code, response.Body.String())
	}
	var result studioHistorySnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func studioHistoryEdits(t *testing.T, handler http.Handler) []studioHistoryEntry {
	t.Helper()
	return studioHistoryState(t, handler).Edits
}

func TestStudioHistoryRecordsSourceAndExternalChangesWithDiffs(t *testing.T) {
	handler, path := studioTestHandler(t)
	if state := studioHistoryState(t, handler); len(state.Edits) != 0 {
		t.Fatalf("loading the score should not create a source edit: %+v", state.Edits)
	}
	valid := strings.Replace(studioScore, "Studio", "Night Studio", 1)
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(studioScore)), Source: valid})
	if response.Code != http.StatusOK {
		t.Fatalf("source save: %d %s", response.Code, response.Body.String())
	}
	state := studioHistoryState(t, handler)
	if len(state.Edits) != 1 || state.Edits[0].Label != "Source saved" || state.Edits[0].RevisionBefore != studioRevision([]byte(studioScore)) || state.Edits[0].RevisionAfter != studioRevision([]byte(valid)) || !strings.Contains(state.Edits[0].Diff, "-title \"Studio\"") || !strings.Contains(state.Edits[0].Diff, "+title \"Night Studio\"") {
		t.Fatalf("source save history: %+v", state.Edits)
	}
	if repeated := studioHistoryState(t, handler); len(repeated.Edits) != 1 {
		t.Fatalf("history duplicated own edit: %+v", repeated.Edits)
	}
	external := valid + "// external note\n"
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	state = studioHistoryState(t, handler)
	if len(state.Edits) != 2 || state.Edits[0].Label != "External file change" || state.Edits[0].RevisionBefore != studioRevision([]byte(valid)) || state.Edits[0].RevisionAfter != studioRevision([]byte(external)) || !strings.Contains(state.Edits[0].Diff, "+// external note") {
		t.Fatalf("external edit history: %+v", state.Edits)
	}
	if repeated := studioHistoryState(t, handler); len(repeated.Edits) != 2 {
		t.Fatalf("history duplicated external edit: %+v", repeated.Edits)
	}
}

func TestStudioHistoryExcludesRejectedWritesAndLabelsGridAndMixerWrites(t *testing.T) {
	handler, path := studioTestHandler(t)
	revision := studioRevision([]byte(studioScore))
	invalid := studioCall(t, handler, "/api/source", studioEdit{Revision: revision, Source: "invalid score"})
	if invalid.Code != http.StatusUnprocessableEntity || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatalf("rejected edit entered history: %d", invalid.Code)
	}
	response := studioCall(t, handler, "/api/toggle", studioEdit{Revision: revision, Pattern: "beat", Lane: "bd", Step: 0})
	if response.Code != http.StatusOK {
		t.Fatalf("grid edit: %d %s", response.Code, response.Body.String())
	}
	state := studioHistoryState(t, handler)
	if len(state.Edits) != 1 || state.Edits[0].Label != "Grid · beat / bd step 1 toggled" {
		t.Fatalf("grid edit history: %+v", state.Edits)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mixerSource := strings.Replace(string(current), "track bass acid {}", "track bass acid { mute = on }", 1)
	response = studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision(current), Source: mixerSource, Label: "Mixer · bass mute enabled"})
	if response.Code != http.StatusOK {
		t.Fatalf("mixer source write: %d %s", response.Code, response.Body.String())
	}
	state = studioHistoryState(t, handler)
	if len(state.Edits) != 2 || state.Edits[0].Label != "Mixer · bass mute enabled" {
		t.Fatalf("mixer write history: %+v", state.Edits)
	}
}

func TestStudioUndoRedoAndRedoClearing(t *testing.T) {
	handler, path := studioTestHandler(t)
	initialRevision := studioRevision([]byte(studioScore))
	edit := studioCall(t, handler, "/api/toggle", studioEdit{Revision: initialRevision, Pattern: "pulse", Step: 0})
	if edit.Code != http.StatusOK {
		t.Fatalf("grid edit: %d %s", edit.Code, edit.Body.String())
	}
	changed, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(changed, []byte("{ . . 5 . }")) {
		t.Fatalf("grid edit was not written: %s %v", changed, err)
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)})
	if undo.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", undo.Code, undo.Body.String())
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, []byte(studioScore)) {
		t.Fatalf("undo did not restore grid source: %s %v", restored, err)
	}
	if state := studioHistoryState(t, handler); !state.CanRedo || len(state.Edits) != 2 || state.Edits[0].Label != "Undo · Grid · pulse step 1 toggled" {
		t.Fatalf("undo state: %+v", state)
	}
	redo := studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision(restored)})
	if redo.Code != http.StatusOK {
		t.Fatalf("redo: %d %s", redo.Code, redo.Body.String())
	}
	redone, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(redone, changed) {
		t.Fatalf("redo did not restore edited source: %s %v", redone, err)
	}

	undo = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(redone)})
	if undo.Code != http.StatusOK {
		t.Fatalf("second undo: %d %s", undo.Code, undo.Body.String())
	}
	restored, _ = os.ReadFile(path)
	newSource := strings.Replace(studioScore, "Studio", "After undo", 1)
	newEdit := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision(restored), Source: newSource})
	if newEdit.Code != http.StatusOK {
		t.Fatalf("new edit after undo: %d %s", newEdit.Code, newEdit.Body.String())
	}
	if state := studioHistoryState(t, handler); state.CanRedo {
		t.Fatalf("new edit did not clear redo: %+v", state)
	}
	redo = studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision([]byte(newSource))})
	if redo.Code != http.StatusConflict {
		t.Fatalf("redo after new edit was accepted: %d %s", redo.Code, redo.Body.String())
	}
}

func TestStudioRevertPatchesAnOlderEditWithoutDroppingLaterChanges(t *testing.T) {
	handler, path := studioTestHandler(t)
	changedTitle := strings.Replace(studioScore, "Studio", "Night Studio", 1)
	save := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(studioScore)), Source: changedTitle})
	if save.Code != http.StatusOK {
		t.Fatalf("source save: %d %s", save.Code, save.Body.String())
	}
	changedGrid := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision([]byte(changedTitle)), Pattern: "pulse", Step: 0})
	if changedGrid.Code != http.StatusOK {
		t.Fatalf("grid edit: %d %s", changedGrid.Code, changedGrid.Body.String())
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	revert := studioCall(t, handler, "/api/history/1/revert", studioEdit{Revision: studioRevision(current)})
	if revert.Code != http.StatusOK {
		t.Fatalf("revert earlier save: %d %s", revert.Code, revert.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`title "Studio"`)) || !bytes.Contains(got, []byte("{ . . 5 . }")) {
		t.Fatalf("revert lost later edit or kept reverted change: %s", got)
	}
}

func TestStudioUndoRejectsStaleRevisionAndKeepsExternalChange(t *testing.T) {
	handler, path := studioTestHandler(t)
	edit := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision([]byte(studioScore)), Pattern: "pulse", Step: 0})
	if edit.Code != http.StatusOK {
		t.Fatalf("grid edit: %d %s", edit.Code, edit.Body.String())
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := strings.Replace(string(current), "Studio", "External", 1) + "// external change\n"
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(current)})
	if response.Code != http.StatusConflict {
		t.Fatalf("stale undo did not conflict: %d %s", response.Code, response.Body.String())
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != external {
		t.Fatalf("stale undo clobbered external change: %s %v", actual, err)
	}
	state := studioHistoryState(t, handler)
	if len(state.Edits) != 2 || state.Edits[0].Label != "External file change" || state.CanRedo {
		t.Fatalf("external change was not kept as a separate source edit: %+v", state)
	}
}

func TestStudioExternalChangeCanBeUndoneAndReverted(t *testing.T) {
	handler, path := studioTestHandler(t)
	external := studioScore + "// external edit\n"
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	state := studioHistoryState(t, handler)
	if len(state.Edits) != 1 || state.Edits[0].Label != "External file change" {
		t.Fatalf("external edit not recorded: %+v", state.Edits)
	}
	response := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision([]byte(external))})
	if response.Code != http.StatusOK {
		t.Fatalf("undo external change: %d %s", response.Code, response.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != studioScore {
		t.Fatalf("external undo did not restore previous source: %s %v", got, err)
	}
}

func TestStudioHistorySeparatesTransportEventsAndKeepsAtLeast200Edits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < studioTransportHistoryLimit+25; i++ {
		s.history.record("landed", "Song advanced to outro", int64(i), 1, "")
	}
	before := []byte(studioScore)
	for i := 0; i < 220; i++ {
		after := append(bytes.Clone(before), []byte("// edit "+strconv.Itoa(i)+"\n")...)
		s.history.recordSourceWrite(before, after, "Source saved", studioHistoryWriteNew, 0)
		before = after
	}
	state := s.history.historySnapshot()
	if len(state.Edits) != 220 || len(state.Events) != studioTransportHistoryLimit {
		t.Fatalf("history rings were not independent: edits=%d events=%d", len(state.Edits), len(state.Events))
	}
	for _, entry := range state.Edits {
		if strings.Contains(entry.Label, "Song advanced") || strings.Contains(entry.Diff, "Song advanced") {
			t.Fatalf("transport activity entered edit history: %+v", entry)
		}
	}
	if state.Events[0].Detail != "Song advanced to outro" {
		t.Fatalf("transport event missing from filtered event log: %+v", state.Events[0])
	}
}

func TestStudioHistoryEndpointKeepsTransportEventsOutOfEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	s.history.record("landed", "Song advanced to outro at bar 9", 9, 1, "")
	state := studioHistoryState(t, s.routes())
	if len(state.Edits) != 0 || len(state.Events) != 1 || state.Events[0].Detail != "Song advanced to outro at bar 9" {
		t.Fatalf("transport event crossed into edit history: %+v", state)
	}
}

func TestStudioHistoryPanelLoadsReusableScriptAndControls(t *testing.T) {
	handler, _ := studioTestHandler(t)
	page := studioCall(t, handler, "/", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("Studio page: %d %s", page.Code, page.Body.String())
	}
	for _, markup := range []string{
		`data-studio-history`, `id="history-undo"`, `id="history-redo"`,
		`id="history-show-transport"`, `Show transport events`, `src="/studio-history.js"`,
	} {
		if !strings.Contains(page.Body.String(), markup) {
			t.Fatalf("History panel missing %q", markup)
		}
	}
	script := studioCall(t, handler, "/studio-history.js", nil)
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "renderHistory") || !strings.Contains(script.Body.String(), "bindKeys") {
		t.Fatalf("History module: %d %s", script.Code, script.Body.String())
	}
}

func TestStudioRevertRefusesAnOverlappingLaterChange(t *testing.T) {
	handler, path := studioTestHandler(t)
	first := strings.Replace(studioScore, "Studio", "Night Studio", 1)
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(studioScore)), Source: first})
	if response.Code != http.StatusOK {
		t.Fatalf("first source save: %d %s", response.Code, response.Body.String())
	}
	second := strings.Replace(first, "Night Studio", "Late Studio", 1)
	response = studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(first)), Source: second})
	if response.Code != http.StatusOK {
		t.Fatalf("second source save: %d %s", response.Code, response.Body.String())
	}
	response = studioCall(t, handler, "/api/history/1/revert", studioEdit{Revision: studioRevision([]byte(second))})
	if response.Code != http.StatusConflict {
		t.Fatalf("overlapping revert was accepted: %d %s", response.Code, response.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != second {
		t.Fatalf("overlapping revert changed the source: %s %v", got, err)
	}
}
