package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func TestStudioArrangementTempoHistoryFreshnessAndCapture(t *testing.T) {
	isolateState(t)
	s, entry, part, owner := printSourcesStudio(t, t.TempDir())
	h := s.routes()
	change := studioEdit{Revision: studioRevision(entry), Action: "set-tempo", Tempo: &edits.SetArrangementTempo{At: "@3.1.1", Value: "96"}}
	if r := studioCall(t, h, "/api/arrange", change); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(owner)
	if !bytes.Contains(after, []byte("tempo @3.1.1 96")) {
		t.Fatal("tempo was not written to its owning source")
	}
	for _, c := range []struct {
		route string
		want  []byte
	}{{"undo", part}, {"redo", after}} {
		if r := studioCall(t, h, "/api/"+c.route, studioEdit{Revision: studioRevision(entry)}); r.Code != 200 {
			t.Fatal(c.route, r.Code, r.Body.String())
		}
		got, _ := os.ReadFile(owner)
		main, _ := os.ReadFile(s.path)
		if !bytes.Equal(got, c.want) || !bytes.Equal(main, entry) {
			t.Fatal("history did not preserve exact source snapshots")
		}
	}
	score, ds, err := project.LoadScore(s.path, nil)
	if err != nil || len(ds) != 0 {
		t.Fatal(ds, err)
	}
	loaded, ds := project.FromScore(score)
	if loaded == nil || len(ds) != 0 || len(loaded.Arrange.Tempos) != 1 || loaded.Arrange.Tempos[0].BPMMilli != 96000 {
		t.Fatalf("reopened tempo: %v %v", loaded, ds)
	}
	var workspace struct {
		SourceRevision string `json:"source_revision"`
	}
	r := studioCall(t, h, "/api/workspace", nil)
	if err := json.Unmarshal(r.Body.Bytes(), &workspace); err != nil || workspace.SourceRevision == "" {
		t.Fatalf("workspace freshness: %v %s", err, r.Body)
	}
	change.SourceRevision, change.Tempo = workspace.SourceRevision, &edits.SetArrangementTempo{OldAt: "@3.1.1", At: "@2.1.1", Value: "137.333"}
	external := append(bytes.Clone(after), []byte("// saved elsewhere\r\n")...)
	if err := os.WriteFile(owner, external, 0600); err != nil {
		t.Fatal(err)
	}
	if r := studioCall(t, h, "/api/arrange", change); r.Code != http.StatusConflict {
		t.Fatalf("stale owning source accepted: %d %s", r.Code, r.Body)
	}
	change.SourceRevision = ""
	// A staged proposal must recheck capture at acceptance, even though its
	// compiled tempo change was valid before recording started.
	r = studioCall(t, h, "/api/proposals", edits.Proposal{Envelope: edits.Envelope{Version: 1, Revision: change.Revision, Intents: []edits.Intent{change.Tempo}}})
	var proposal struct {
		ID string `json:"id"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &proposal) != nil || proposal.ID == "" {
		t.Fatalf("proposal: %d %s", r.Code, r.Body)
	}
	s.captureID = "synthetic-recording-open"
	defer func() { s.captureID = "" }()
	for _, call := range []struct {
		route string
		body  any
	}{
		{"/api/arrange", change},
		{"/api/intents", edits.Envelope{Version: 1, Revision: change.Revision, Intents: []edits.Intent{change.Tempo}}},
		{"/api/proposals/" + proposal.ID + "/accept", map[string]any{}},
	} {
		before := snapshotStudioState(t, s)
		r := studioCall(t, h, call.route, call.body)
		if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "finish the recording") {
			t.Fatalf("capture edit %s: %d %s", call.route, r.Code, r.Body)
		}
		current, _ := os.ReadFile(owner)
		state := snapshotStudioState(t, s)
		if !bytes.Equal(current, external) || state.history != before.history || state.log != before.log {
			t.Fatal("refused tempo edit left a trace")
		}
	}
}
