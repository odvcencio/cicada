package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

func studioHistoryEvents(t *testing.T, handler http.Handler) []studioHistoryEntry {
	t.Helper()
	response := studioCall(t, handler, "/api/history", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("history status: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Events []studioHistoryEntry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Events
}

func TestStudioHistoryUsesRenderedTransportBar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	s.transport.stream, s.transport.playing = stream, true
	updated := strings.Replace(studioScore, "Studio", "Second bar", 1)
	handler := s.routes()
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(studioScore)), Source: updated})
	if response.Code != http.StatusOK {
		t.Fatalf("second-bar edit: %d %s", response.Code, response.Body.String())
	}
	events := studioHistoryEvents(t, handler)
	if events[0].Bar != 2 || events[0].Step < 1 || events[0].Step > 16 {
		t.Fatalf("history lost transport position: %+v", events[0])
	}
}

func TestStudioHistoryTracksSourceAndExternalEditsWithoutDuplicates(t *testing.T) {
	handler, path := studioTestHandler(t)
	events := studioHistoryEvents(t, handler)
	if len(events) != 1 || events[0].Kind != "loaded" || events[0].Bar != 0 {
		t.Fatalf("initial history: %+v", events)
	}
	valid := strings.Replace(studioScore, "Studio", "Night Studio", 1)
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision([]byte(studioScore)), Source: valid})
	if response.Code != http.StatusOK {
		t.Fatalf("source save: %d %s", response.Code, response.Body.String())
	}
	events = studioHistoryEvents(t, handler)
	if len(events) != 2 || events[0].Kind != "edit" || events[0].Detail != "Source edited" || events[0].Seq <= events[1].Seq {
		t.Fatalf("source edit history: %+v", events)
	}
	if repeated := studioHistoryEvents(t, handler); len(repeated) != 2 {
		t.Fatalf("history duplicated own edit: %+v", repeated)
	}
	external := valid + "// external note\n"
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	events = studioHistoryEvents(t, handler)
	if len(events) != 3 || events[0].Kind != "external" || events[0].Detail != "Score changed outside Studio" {
		t.Fatalf("external edit history: %+v", events)
	}
	if repeated := studioHistoryEvents(t, handler); len(repeated) != 3 {
		t.Fatalf("history duplicated external edit: %+v", repeated)
	}
}

func TestStudioHistoryExcludesRejectedEditAndNamesGridStep(t *testing.T) {
	handler, _ := studioTestHandler(t)
	revision := studioRevision([]byte(studioScore))
	invalid := studioCall(t, handler, "/api/source", studioEdit{Revision: revision, Source: "invalid score"})
	if invalid.Code != http.StatusUnprocessableEntity || len(studioHistoryEvents(t, handler)) != 1 {
		t.Fatalf("rejected edit entered history: %d", invalid.Code)
	}
	response := studioCall(t, handler, "/api/toggle", studioEdit{Revision: revision, Pattern: "beat", Lane: "bd", Step: 0})
	if response.Code != http.StatusOK {
		t.Fatalf("grid toggle: %d %s", response.Code, response.Body.String())
	}
	events := studioHistoryEvents(t, handler)
	if len(events) != 2 || events[0].Kind != "edit" || events[0].Detail != "beat / bd step 1 toggled" {
		t.Fatalf("grid edit history: %+v", events)
	}
}
