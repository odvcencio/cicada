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
			if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "Stop playback") {
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
	if protected.Code != http.StatusConflict || !strings.Contains(protected.Body.String(), "unsaved performances") {
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
		if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "Stop playback") {
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
	if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "Stop playback") {
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

func TestTrackIdentityComparisonDetectsSwappedNames(t *testing.T) {
	before := &project.Project{Tracks: []project.Track{{ID: "bass"}, {ID: "lead"}}}
	after := &project.Project{Tracks: []project.Track{{ID: "lead"}, {ID: "bass"}}}
	if sameTrackIdentities(before, after) {
		t.Fatal("swapping names must not appear identity-preserving")
	}
	if !sameTrackIdentities(before, before) {
		t.Fatal("unchanged identities must compare equal")
	}
}
