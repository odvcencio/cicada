package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/takejournal"
)

func TestTakeRoutesAcceptUnchangedSelection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, action := range []string{"select", "recover"} {
		t.Run(action, func(t *testing.T) {
			s := newTakeStudio(t, t.TempDir())
			id := captureTestTake(t, s)
			selected := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: id, Revision: studioRevision([]byte(audioTakeScore))})
			if selected.Code != http.StatusOK {
				t.Fatalf("initial selection: %d %s", selected.Code, selected.Body.String())
			}
			current, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if action == "recover" {
				external := append(bytes.Clone(current), []byte("// intervening edit\n")...)
				if err := os.WriteFile(s.path, external, 0600); err != nil {
					t.Fatal(err)
				}
				conflict := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: id, Revision: studioRevision(current)})
				take, err := s.takes.Get(id)
				if conflict.Code != http.StatusConflict || err != nil || take.Stage != takejournal.Conflict {
					t.Fatalf("real conflict: %d %s; take=%+v err=%v", conflict.Code, conflict.Body.String(), take, err)
				}
				current = external
			}
			history := s.history.historySnapshot()
			log, err := os.ReadFile(editlog.Path(s.path))
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(s.path)
			if err != nil {
				t.Fatal(err)
			}
			var stages []takejournal.Stage
			s.takes.Boundary = func(stage takejournal.Stage) { stages = append(stages, stage) }
			for attempt := 0; attempt < 2; attempt++ {
				response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: action, TakeID: id, Revision: studioRevision(current)})
				if response.Code != http.StatusOK {
					t.Fatalf("unchanged %s #%d: %d %s", action, attempt, response.Code, response.Body.String())
				}
				take, err := s.takes.Get(id)
				if err != nil || take.Stage != takejournal.Committed || take.Before != studioRevision(current) || take.After != studioRevision(current) {
					t.Fatalf("unchanged selection not completed: %+v err=%v", take, err)
				}
				// The take route retains its legacy projection body (no generic
				// edit response or unchanged flag).
				want, err := json.Marshal(map[string]any{"revision": studioRevision(current), "source": string(current), "take": id, "takes": s.takeProjection(), "activeCapture": s.captureID})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()), want) {
					t.Fatalf("take response\n got: %s\nwant: %s", response.Body.Bytes(), want)
				}
			}
			// The existing recovery pass rechecks immutable asset links. The
			// source transition must complete without conflict or exchange.
			wantStages := []takejournal.Stage{takejournal.BlobLinked, takejournal.TakeLinked, takejournal.Prepared, takejournal.Source, takejournal.Committed, takejournal.BlobLinked, takejournal.TakeLinked, takejournal.Prepared, takejournal.Source, takejournal.Committed}
			if !reflect.DeepEqual(stages, wantStages) {
				t.Fatalf("no-op must complete the journal without conflict or source exchange: %v", stages)
			}
			after, err := os.ReadFile(s.path)
			if err != nil || !bytes.Equal(after, current) {
				t.Fatalf("no-op changed source: %v", err)
			}
			afterInfo, err := os.Stat(s.path)
			if err != nil || !os.SameFile(info, afterInfo) || !info.ModTime().Equal(afterInfo.ModTime()) {
				t.Fatalf("no-op exchanged source: %v", err)
			}
			afterLog, err := os.ReadFile(editlog.Path(s.path))
			if err != nil || !bytes.Equal(afterLog, log) || !reflect.DeepEqual(s.history.historySnapshot(), history) {
				t.Fatalf("no-op recorded an edit: %v", err)
			}
		})
	}
}
