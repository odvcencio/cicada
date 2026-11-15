package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func TestTrackRenameRefusesRunningAndPausedTransport(t *testing.T) {
	isolateState(t)
	s, handler, path := proposalFixture(t, studioMixerScore)
	s.transport.audioBackend = "null"
	rename := &edit.Rename{Entity: "track:bass", Name: "low"}
	env := edit.Envelope{Version: 1, Revision: studioRevision([]byte(studioMixerScore)), Intents: []edit.Intent{rename}}
	id := stageProposal(t, handler, studioMixerScore, "Rename", rename)
	command := func(action string) {
		t.Helper()
		r := studioCall(t, handler, "/api/transport", map[string]string{"action": action})
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, r.Code, r.Body.String())
		}
	}
	command("play")
	for _, state := range []string{"playing", "paused"} {
		if state == "paused" {
			command("pause")
		}
		for _, path := range []string{"/api/intents", "/api/proposals/" + id + "/accept"} {
			var body any = env
			if strings.Contains(path, "proposals") {
				body = map[string]any{}
			}
			r := studioCall(t, handler, path, body)
			if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "Stop playback") || !strings.Contains(r.Body.String(), "track_identity_busy") {
				t.Fatalf("%s %s: %d %s", state, path, r.Code, r.Body.String())
			}
		}
		if current, _ := os.ReadFile(path); string(current) != studioMixerScore {
			t.Fatal("refusal changed score")
		}
	}
	command("stop")
	r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
	if r.Code != http.StatusOK {
		t.Fatalf("accept after Stop: %d %s", r.Code, r.Body.String())
	}
	changed, _ := os.ReadFile(path)
	if !strings.Contains(string(changed), "track low") {
		t.Fatal("rename did not commit")
	}
	protected := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed), ProtectTrackIdentities: true})
	if protected.Code != http.StatusUnprocessableEntity || !strings.Contains(protected.Body.String(), "retained_take_track_required") {
		t.Fatalf("protected history: %d %s", protected.Code, protected.Body.String())
	}
	entry, ok := s.history.undoEntry()
	if !ok {
		t.Fatal("rename missing from history")
	}
	command("play")
	// An unrelated parameter edit and Undo remain available during playback.
	param := edit.Envelope{Version: 1, Revision: studioRevision(changed), Intents: []edit.Intent{&edit.SetParam{Entity: "param:low.level", Value: json.RawMessage(`-3`)}}}
	if r = studioCall(t, handler, "/api/intents", param); r.Code != http.StatusOK {
		t.Fatalf("parameter edit: %d %s", r.Code, r.Body.String())
	}
	withParam, _ := os.ReadFile(path)
	if r = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(withParam), ProtectTrackIdentities: true}); r.Code != http.StatusOK {
		t.Fatalf("parameter Undo during playback: %d %s", r.Code, r.Body.String())
	}
	for _, route := range []string{"/api/undo", fmt.Sprintf("/api/history/%d/revert", entry.ID)} {
		r = studioCall(t, handler, route, studioEdit{Revision: studioRevision(changed)})
		if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "Stop playback") || !strings.Contains(r.Body.String(), "track_identity_busy") {
			t.Fatalf("%s while playing: %d %s", route, r.Code, r.Body.String())
		}
	}
	if current, _ := os.ReadFile(path); string(current) != string(changed) {
		t.Fatal("refused history changed source")
	}
	command("stop")
	r = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)})
	if r.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", r.Code, r.Body.String())
	}
	if current, _ := os.ReadFile(path); string(current) != studioMixerScore {
		t.Fatal("Undo did not restore exact score")
	}
	command("play")
	r = studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision([]byte(studioMixerScore))})
	if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "Stop playback") {
		t.Fatalf("redo while playing: %d %s", r.Code, r.Body.String())
	}
	command("stop")
	r = studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision([]byte(studioMixerScore))})
	if r.Code != http.StatusOK {
		t.Fatalf("redo after stop: %d %s", r.Code, r.Body.String())
	}
	redone, _ := os.ReadFile(path)
	if string(redone) != string(changed) {
		t.Fatal("redo did not restore renamed source")
	}
	if r = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(redone)}); r.Code != http.StatusOK {
		t.Fatalf("undo redone rename: %d %s", r.Code, r.Body.String())
	}
	// The direct agent interface follows the same guarded commit path.
	r = studioCall(t, handler, "/api/intents", env)
	if r.Code != http.StatusOK {
		t.Fatalf("direct rename after Stop: %d %s", r.Code, r.Body.String())
	}
	changed, _ = os.ReadFile(path)
	if r = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)}); r.Code != http.StatusOK {
		t.Fatalf("direct rename Undo: %d %s", r.Code, r.Body.String())
	}
	if current, _ := os.ReadFile(path); string(current) != studioMixerScore {
		t.Fatal("direct rename Undo did not restore exact score")
	}
}

func TestHistoryTrackIdentityComparison(t *testing.T) {
	before := &project.Project{Tracks: []project.Track{{ID: "bass"}, {ID: "lead"}}}
	for _, tc := range []struct {
		name           string
		after          *project.Project
		removed, added bool
	}{
		{"unchanged", before, false, false},
		{"reorder only", &project.Project{Tracks: []project.Track{{ID: "lead"}, {ID: "bass"}}}, false, false},
		{"remove A and add B", &project.Project{Tracks: []project.Track{{ID: "low"}, {ID: "lead"}}}, true, true},
		{"rename A and add C", &project.Project{Tracks: []project.Track{{ID: "low"}, {ID: "lead"}, {ID: "vox"}}}, true, true},
		{"add only", &project.Project{Tracks: []project.Track{{ID: "bass"}, {ID: "lead"}, {ID: "vox"}}}, false, true},
		{"remove only", &project.Project{Tracks: []project.Track{{ID: "bass"}}}, true, false},
		{"missing", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removed, added := historyTrackChanges(before, tc.after)
			if removed != tc.removed || added != tc.added {
				t.Fatalf("got removed=%v added=%v, want %v %v", removed, added, tc.removed, tc.added)
			}
		})
	}
}

func TestHistoryUndoAddedTrackWhilePlaying(t *testing.T) {
	isolateState(t)
	s, handler, path := proposalFixture(t, studioMixerScore)
	s.transport.audioBackend = "null"
	if r := studioCall(t, handler, "/api/transport", map[string]string{"action": "play"}); r.Code != http.StatusOK {
		t.Fatalf("play: %d %s", r.Code, r.Body.String())
	}
	defer studioCall(t, handler, "/api/transport", map[string]string{"action": "stop"})
	env := edit.Envelope{Version: 1, Revision: studioRevision([]byte(studioMixerScore)), Intents: []edit.Intent{&edit.AddAudioTrack{Name: "vox"}}}
	if r := studioCall(t, handler, "/api/intents", env); r.Code != http.StatusOK {
		t.Fatalf("add: %d %s", r.Code, r.Body.String())
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(current), ProtectTrackIdentities: true}); r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "retained_take_track_required") {
		t.Fatalf("protected undo add: %d %s", r.Code, r.Body.String())
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(current)}); r.Code != http.StatusOK {
		t.Fatalf("undo add: %d %s", r.Code, r.Body.String())
	}
	if current, err = os.ReadFile(path); err != nil || string(current) != studioMixerScore {
		t.Fatalf("undo did not restore score: %v %s", err, current)
	}
}

func TestHistoryUndoInvalidExternalScoreWhilePlaying(t *testing.T) {
	isolateState(t)
	s, handler, path := proposalFixture(t, studioMixerScore)
	s.transport.audioBackend = "null"
	if r := studioCall(t, handler, "/api/transport", map[string]string{"action": "play"}); r.Code != http.StatusOK {
		t.Fatalf("play: %d %s", r.Code, r.Body.String())
	}
	defer studioCall(t, handler, "/api/transport", map[string]string{"action": "stop"})
	broken := []byte(studioMixerScore + "\ntrack ???\n")
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	// The last good project is stale; the stream still plays the original IDs.
	s.mu.Lock()
	s.lastGoodProject = &project.Project{Tracks: []project.Track{{ID: "stale"}}}
	s.mu.Unlock()
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(broken)}); r.Code != http.StatusOK {
		t.Fatalf("undo invalid score: %d %s", r.Code, r.Body.String())
	}
	if current, err := os.ReadFile(path); err != nil || string(current) != studioMixerScore {
		t.Fatalf("undo did not restore score: %v %s", err, current)
	}
}
