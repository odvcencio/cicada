package main

import (
	"bytes"
	"net/http"
	"os"
	"testing"
)

func TestLibraryNoteRecordingChecksAndCommitsWithOneHistoryEntry(t *testing.T) {
	for _, timing := range []string{"grid", "played"} {
		t.Run(timing, func(t *testing.T) {
			isolateState(t)
			source := []byte("cicada 2\ntrack keys piano {}\npattern fresh notes { . . . . }\nscene main {}\nsong { main*4 }\n")
			path := studioTestPath(t, string(source))
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			h := s.routes()
			request := studioEdit{Revision: studioRevision(source), SourceRevision: studioProjectReceipt(t, path), Timing: timing, Recordings: []studioTakeRecording{{Track: "keys", Pattern: "fresh", Notes: []studioTakeNote{{Tick: 3857, EndTick: 4200, Note: 60, Velocity: 90}}}}}
			r := studioCall(t, h, "/api/record", request)
			if r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
			captured, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(captured, source) || !bytes.Contains(captured, []byte("scene main {}\nsong { main*4 }")) || len(studioHistoryEdits(t, h)) != 1 {
				t.Fatal("library recording changed song bindings or history scope")
			}
			if r := studioCall(t, h, "/api/record", request); r.Code != http.StatusConflict {
				t.Fatal("stale library take was applied", r.Code, r.Body.String())
			}
			for _, step := range []struct {
				route        string
				before, want []byte
			}{{"undo", captured, source}, {"redo", source, captured}} {
				if r := studioCall(t, h, "/api/"+step.route, studioEdit{Revision: studioRevision(step.before)}); r.Code != 200 {
					t.Fatal(step.route, r.Code, r.Body.String())
				}
				got, _ := os.ReadFile(path)
				if !bytes.Equal(got, step.want) {
					t.Fatal("history lost the unused library pattern", step.route)
				}
			}
		})
	}
}
