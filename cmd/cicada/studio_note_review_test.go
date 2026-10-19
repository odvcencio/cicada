package main

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/edit"
)

func TestNoteTakeCheckAndCollisionRefusalLeaveProjectUntouched(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, handler, path := proposalFixture(t, studioScore)
	before := noopFiles(t, filepath.Dir(path), os.Getenv("XDG_CONFIG_HOME"))
	recording := studioTakeRecording{Track: "bass", Pattern: "pulse", Notes: []studioTakeNote{{Note: 60, Velocity: 90, EndTick: 60}}}
	request := edit.Envelope{Version: edit.EnvelopeVersion, Revision: studioRevision([]byte(studioScore)), DryRun: true, Intents: []edit.Intent{&edit.RecordTake{Recordings: editRecordings([]studioTakeRecording{recording})}}}
	response := studioCall(t, handler, "/api/intents", request)
	if response.Code != http.StatusOK {
		t.Fatalf("note check: %d %s", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path), os.Getenv("XDG_CONFIG_HOME"))) || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("checking notes changed project or history")
	}
	recording.Notes = append(recording.Notes, studioTakeNote{Tick: 20, EndTick: 80, Note: 64, Velocity: 100})
	response = studioCall(t, handler, "/api/record", studioEdit{Revision: request.Revision, Recordings: []studioTakeRecording{recording}})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("collision accepted: %d %s", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path), os.Getenv("XDG_CONFIG_HOME"))) || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("collision refusal changed project or history")
	}
}
