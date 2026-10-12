package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
)

func TestM4RoutesCommitPublicIntents(t *testing.T) {
	for _, c := range m4RouteCases() {
		// One authored write per operation, plus the separate file session.
		if !strings.HasSuffix(c.name, `/"\n"`) {
			continue
		}
		kind := map[string]string{
			"song/move forward": "movesongentry", "song/bars explicit": "setsongbars", "song/append": "appendsongentry",
			"song/duplicate": "duplicatesongentry", "song/delete": "deletesongentry", "song/scene": "setsongscene",
			"automation/set": "setscenesetting", "automation/remove": "removescenesetting",
			"project/spelling and precision": "setprojectsettings", "clip/track": "addaudiotrack", "clip/fades": "setclipsettings", "clip/bind": "bindscene",
			"source/write": "replacetext", "files/write": "replacetext", "files/part write": "replacetext",
		}[strings.TrimSuffix(c.name, `/"\n"`)]
		if kind == "" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			s, path := m4Session(t, c)
			body := c.body
			body.Revision, body.Author = studioRevision([]byte(c.source)), "tester"
			if !c.part {
				body.Session = "gesture-session"
			}
			response := studioCall(t, s.routes(), c.route, body)
			if response.Code != 200 {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			records, skipped, err := editlog.ReadLog(editlog.Path(path))
			if err != nil || skipped != 0 || len(records) != 1 || len(records[0].Intents) != 1 {
				t.Fatalf("edit log: %+v, skipped %d, %v", records, skipped, err)
			}
			var in map[string]any
			if err := json.Unmarshal(records[0].Intents[0], &in); err != nil {
				t.Fatal(err)
			}
			if in["kind"] != kind || records[0].Author != "tester" {
				t.Fatalf("public intent %v in %+v", in, records[0])
			}
			if c.part {
				target := s.fileSessions[path]
				if target.sessionID == "" || target.sessionID == s.sessionID || records[0].Session != target.sessionID {
					t.Fatalf("per-file session %q, root %q, log %q", target.sessionID, s.sessionID, records[0].Session)
				}
				main, _ := os.ReadFile(s.path)
				if !bytes.Contains(main, []byte("song { main }")) {
					t.Fatal("part save changed entry")
				}
			} else if records[0].Session != "gesture-session" {
				t.Fatalf("session %q", records[0].Session)
			}
		})
	}
}

func TestM4RoutesReturnCanonicalConflicts(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range m4RouteCases() {
		if c.wantStatus != 200 || seen[c.route] {
			continue
		}
		seen[c.route] = true
		t.Run(c.route, func(t *testing.T) {
			s, path := m4Session(t, c)
			body := c.body
			body.Revision = "stale"
			response := studioCall(t, s.routes(), c.route, body)
			var reply map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if response.Code != 409 || reply["error"] != "score changed on disk; reload before saving" || reply["source"] != c.source || reply["revision"] != studioRevision([]byte(c.source)) {
				t.Fatalf("canonical conflict: %d %+v", response.Code, reply)
			}
			after, _ := os.ReadFile(path)
			if string(after) != c.source || len(s.history.historySnapshot().Edits) != 0 {
				t.Fatal("conflict changed bytes/history")
			}
		})
	}
	if len(seen) != 6 {
		t.Fatalf("checked %d routes", len(seen))
	}
}

func TestM4CommentMoveUndoRestoresExactSource(t *testing.T) {
	for _, c := range m4RouteCases() {
		if !strings.HasPrefix(c.name, "song/comment move/") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			s, path := m4Session(t, c)
			body := c.body
			body.Revision = studioRevision([]byte(c.source))
			r := studioCall(t, s.routes(), c.route, body)
			if r.Code != 200 {
				t.Fatalf("comment move: %d %s", r.Code, r.Body.String())
			}
			after, _ := os.ReadFile(path)
			r = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
			restored, _ := os.ReadFile(path)
			if r.Code != 200 || string(restored) != c.source {
				t.Fatalf("undo %d, source %q", r.Code, restored)
			}
		})
	}
}
