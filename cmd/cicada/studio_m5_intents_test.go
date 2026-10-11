package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/project"
)

func assertM5IntentLog(t *testing.T, path, kind, author, session string) {
	t.Helper()
	records, skipped, err := editlog.ReadLog(editlog.Path(path))
	if err != nil || skipped != 0 || len(records) != 1 || len(records[0].Intents) != 1 {
		t.Fatalf("intent log: %+v skipped=%d err=%v", records, skipped, err)
	}
	var in map[string]any
	if err = json.Unmarshal(records[0].Intents[0], &in); err != nil {
		t.Fatal(err)
	}
	if in["kind"] != kind || records[0].Author != author || records[0].Session != session {
		t.Fatalf("attribution/kind: %+v %+v", records[0], in)
	}
	if kind == "insertlibraryitem" && in["itemkind"] != "effect" {
		t.Fatalf("library item type lost: %+v", in)
	}
}

func TestM5RoutesCommitAttributedIntents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, c := range []struct {
		route, source, kind string
		body                studioEdit
	}{
		{"/api/instrument", presetEditScore, "addpreset", studioEdit{Action: "add-preset", Pattern: "warm-pad", NewName: "pad", Track: "keys"}},
		{"/api/library/insert", studioLibraryScore, "insertlibraryitem", studioEdit{Path: "std/fx", Item: "delay", Track: "lead"}},
		{"/api/library/save", studioLibraryScore, "savepreset", studioEdit{Track: "lead", Name: "bright"}},
		{"/api/record", studioScore, "recordtake", studioEdit{Track: "bass", Pattern: "pulse", Take: []studioTakeNote{{Note: 60, Velocity: 90, EndTick: 120}}}},
	} {
		t.Run(c.kind, func(t *testing.T) {
			path := studioTestPath(t, c.source)
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.shutdown()
			body := c.body
			body.Revision = studioRevision([]byte(c.source))
			body.Author = "tester"
			body.Session = "s1"
			r := studioCall(t, s.routes(), c.route, body)
			if r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			assertM5IntentLog(t, path, c.kind, "tester", "s1")
			stale := studioCall(t, s.routes(), c.route, body)
			var reply map[string]any
			if err = json.Unmarshal(stale.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if stale.Code != 409 || reply["source"] != string(current) || reply["revision"] != studioRevision(current) {
				t.Fatalf("canonical conflict: %d %s", stale.Code, stale.Body.String())
			}
		})
	}
}

func TestPublishRecordedAndSelectTakeLogIntents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Run("publishrecorded", func(t *testing.T) {
		path := studioTestPath(t, studioScore)
		s, err := newStudio(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.shutdown()
		pack, err := recording.Build("captured", []recording.Hit{{Rate: 48000, Root: 60, Peak: .5, SourceSHA256: strings.Repeat("a", 64), PCM: []float32{0, .25, .5, .25, 0}}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		s.publishRecorded(response, pack, 60, studioEdit{Revision: studioRevision([]byte(studioScore)), Author: "tester", Session: "s1"}, nil)
		if response.Code != 200 {
			t.Fatalf("publish: %d %s", response.Code, response.Body.String())
		}
		assertM5IntentLog(t, path, "publishrecorded", "tester", "s1")
	})
	t.Run("selecttake", func(t *testing.T) {
		s := newTakeStudio(t, t.TempDir())
		defer s.shutdown()
		id := captureTestTake(t, s)
		if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); err != nil {
			t.Fatal(err)
		}
		assertM5IntentLog(t, s.path, "selecttake", "local", s.sessionID)
	})
}

func TestLibraryInsertValidatesPendingPinsBeforeCommit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h, path := libraryStudio(t)
	pinsPath := filepath.Join(filepath.Dir(path), "cicada.sum")
	original := studioCompile
	defer func() { studioCompile = original }()
	candidateCompiles := 0
	studioCompile = func(filename string, source []byte, overrides map[string][]byte) (*project.Project, error) {
		if strings.Contains(string(source), `import "std/fx"`) {
			candidateCompiles++
			if _, err := os.Stat(pinsPath); !os.IsNotExist(err) {
				t.Fatalf("pins written before source validation: %v", err)
			}
			if len(overrides[pinsPath]) == 0 {
				t.Fatal("candidate lacks pending pins")
			}
		}
		return original(filename, source, overrides)
	}
	r := studioCall(t, h, "/api/library/insert", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: "std/fx", Item: "delay", Track: "lead"})
	if r.Code != 200 || candidateCompiles != 1 {
		t.Fatalf("%d %s; candidate compiles=%d", r.Code, r.Body.String(), candidateCompiles)
	}
	if pins, err := os.ReadFile(pinsPath); err != nil || len(pins) == 0 {
		t.Fatalf("pins not committed: %v", err)
	}
}
