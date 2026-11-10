package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/edit"
)

// notesStudioScore is a played performance of count off-grid notes on an
// authored synth, one 1/16 apart.
func notesStudioScore(count int) (string, []edit.NoteRef) {
	var body strings.Builder
	var refs []edit.NoteRef
	for i := range count {
		at := int64(i*240 + 11 + i%3)
		pitch := 40 + i%48
		fmt.Fprintf(&body, "  note %d at=%dticks length=120ticks velocity=%d;\n", pitch, at, 60+i%60)
		refs = append(refs, edit.NoteRef{At: at, Pitch: pitch, Length: 120, Velocity: 60 + i%60})
	}
	length := (int64(count)*240 + 3839) / 3840 * 3840
	return fmt.Sprintf("cicada 2\ntitle \"Notes\"\ninstrument glass { voice poly { out = sine(pitch) * env(gate, 150ms) * 0.1 } }\ntrack keys glass {}\n"+
		"pattern take performance length=%dticks {\n  // played in\n%s} // keep\nscene main { keys=take }\nsong { main }\n", length, body.String()), refs
}

func notesStudio(t *testing.T, source string) (http.Handler, string) {
	t.Helper()
	isolateState(t)
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	return handler, path
}

func notesRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func notesCall(t *testing.T, handler http.Handler, path, pattern string, in *edit.EditNotes, receipt bool) (int, map[string]any) {
	t.Helper()
	body := studioEdit{Revision: studioRevision(notesRead(t, path)), Action: "notes", Pattern: pattern, Notes: in}
	if receipt {
		body.SourceRevision = studioProjectReceipt(t, path)
	}
	r := studioCall(t, handler, "/api/pattern", body)
	var reply map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	return r.Code, reply
}

func TestStudioNotesBatchIsOneCommitAndOneUndo(t *testing.T) {
	source, refs := notesStudioScore(16)
	handler, path := notesStudio(t, source)
	before := notesRead(t, path)
	quantize := &edit.EditNotes{Op: "quantize", Grid: 240, Notes: refs}
	if code, reply := notesCall(t, handler, path, "take", quantize, false); code != http.StatusConflict || !bytes.Equal(notesRead(t, path), before) {
		t.Fatalf("a batch without a source receipt was not refused: %d %v", code, reply)
	}
	code, reply := notesCall(t, handler, path, "take", quantize, true)
	if code != http.StatusOK || reply["unchanged"] == true {
		t.Fatalf("quantize: %d %v", code, reply)
	}
	after := notesRead(t, path)
	for i, ref := range refs {
		if want := fmt.Sprintf("note %d at=%dticks length=120ticks velocity=%d;", ref.Pitch, i*240, ref.Velocity); !bytes.Contains(after, []byte(want)) {
			t.Fatalf("note %d is not on the grid: missing %q\n%s", i, want, after)
		}
	}
	if !bytes.Contains(after, []byte("// played in")) || !bytes.Contains(after, []byte("} // keep")) {
		t.Fatalf("quantize lost a comment:\n%s", after)
	}
	history := studioHistoryEdits(t, handler)
	if len(history) != 1 || history[0].Label != "Notes · take · 16 notes quantized to 1/16" {
		t.Fatalf("a 16-note batch must be one history entry: %+v", history)
	}
	// The stale revision of the pre-edit page is refused and writes nothing.
	stale := studioEdit{Revision: studioRevision(before), SourceRevision: studioProjectReceipt(t, path), Action: "notes", Pattern: "take", Notes: quantize}
	if r := studioCall(t, handler, "/api/pattern", stale); r.Code != http.StatusConflict || !bytes.Equal(notesRead(t, path), after) {
		t.Fatalf("stale batch: %d %s", r.Code, r.Body.String())
	}
	// The notes the old page named no longer exist: 422, nothing written.
	if code, reply := notesCall(t, handler, path, "take", quantize, true); code != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(reply["error"]), "a selected note changed") || !bytes.Equal(notesRead(t, path), after) {
		t.Fatalf("changed notes: %d %v", code, reply)
	}
	// Quantizing again changes nothing and records no history.
	code, reply = notesCall(t, handler, path, "take", &edit.EditNotes{Op: "quantize", Grid: 240}, true)
	if code != http.StatusOK || reply["unchanged"] != true || reply["message"] != "Already on the grid." || len(studioHistoryEdits(t, handler)) != 1 {
		t.Fatalf("repeat quantize: %d %v", code, reply)
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(after)}); r.Code != http.StatusOK || !bytes.Equal(notesRead(t, path), before) {
		t.Fatalf("one Undo did not restore the exact source: %d %s", r.Code, r.Body.String())
	}
	if r := studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision(before)}); r.Code != http.StatusOK || !bytes.Equal(notesRead(t, path), after) {
		t.Fatalf("Redo did not restore the quantized source: %d %s", r.Code, r.Body.String())
	}
}

func TestStudioNotesRefusalsLeaveTheScore(t *testing.T) {
	base, _ := notesStudioScore(1)
	source := strings.Replace(base, "  note 40 at=11ticks length=120ticks velocity=60;\n", "  note 40 at=130ticks length=200ticks velocity=60;\n  note 40 at=350ticks length=100ticks velocity=60;\n", 1)
	handler, path := notesStudio(t, source)
	before := notesRead(t, path)
	for _, c := range []struct {
		in   *edit.EditNotes
		code int
		want string
	}{
		{&edit.EditNotes{Op: "quantize", Grid: 240}, http.StatusUnprocessableEntity, "note 40 (E2) at tick 350: quantize would put it at tick 240 to 340 as E2, over the E2 at tick 240 to 440"},
		{&edit.EditNotes{Op: "stretch"}, http.StatusUnprocessableEntity, "choose a note operation"},
		{&edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 130, Pitch: 40, Length: 200, Velocity: 60}}, DT: 4000}, http.StatusUnprocessableEntity, "past the loop end"},
	} {
		code, reply := notesCall(t, handler, path, "take", c.in, true)
		if code != c.code || !strings.Contains(fmt.Sprint(reply["error"]), c.want) || !bytes.Equal(notesRead(t, path), before) {
			t.Fatalf("%s: %d %v", c.in.Op, code, reply)
		}
	}
	// A note refusal names its note as data as well as in the message.
	code, reply := notesCall(t, handler, path, "take", &edit.EditNotes{Op: "quantize", Grid: 240}, true)
	if note, _ := reply["note"].(map[string]any); code != http.StatusUnprocessableEntity || reply["code"] != "CICADA-NOTES" || reply["reason"] != "overlap" || note["at"] != float64(350) || note["pitch"] != float64(40) || note["step"] != nil {
		t.Fatalf("structured refusal: %d %v", code, reply)
	}
	if r := studioCall(t, handler, "/api/pattern", studioEdit{Revision: studioRevision(before), SourceRevision: studioProjectReceipt(t, path), Action: "notes", Pattern: "take"}); r.Code != http.StatusBadRequest {
		t.Fatalf("a notes action without an edit: %d %s", r.Code, r.Body.String())
	}
	if len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("a refused batch wrote history")
	}
}

func TestStudioNotesEditTheOwningFileOfAMultiFileProject(t *testing.T) {
	isolateState(t)
	for _, newline := range []string{"\n", "\r\n"} {
		s, before := gridSourcesStudio(t, newline)
		owner := filepath.Join(filepath.Dir(s.path), "drums.cicada")
		body := studioEdit{Revision: studioRevision(before[s.path]), SourceRevision: studioProjectReceipt(t, s.path), Action: "notes", Pattern: "beat", Notes: &edit.EditNotes{Op: "quantize", Grid: 960}}
		if r := studioCall(t, s.routes(), "/api/pattern", body); r.Code != http.StatusOK {
			t.Fatalf("multi-file quantize: %d %s", r.Code, r.Body.String())
		}
		for path, original := range before {
			data := notesRead(t, path)
			if path == owner {
				want := strings.Replace(string(original), "sd: ..x.....", "sd: ....x...", 1)
				if string(data) != want {
					t.Fatalf("owning file:\n%q\nwant\n%q", data, want)
				}
			} else if !bytes.Equal(data, original) {
				t.Fatalf("changed another source %s", path)
			}
		}
		if len(studioHistoryEdits(t, s.routes())) != 1 {
			t.Fatal("multi-file batch split history")
		}
		if r := studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(before[s.path])}); r.Code != http.StatusOK {
			t.Fatalf("undo: %d %s", r.Code, r.Body.String())
		}
		for path, want := range before {
			if !bytes.Equal(notesRead(t, path), want) {
				t.Fatalf("Undo lost exact source %s", path)
			}
		}
	}
}

// TestStudioNotesQuantize500Timing reports the server time of one 500-note
// quantize through /api/pattern: decode, compile, edit, validate, write and
// history. Run with -v to read the measurement.
func TestStudioNotesQuantize500Timing(t *testing.T) {
	source, _ := notesStudioScore(500)
	handler, path := notesStudio(t, source)
	started := time.Now()
	code, reply := notesCall(t, handler, path, "take", &edit.EditNotes{Op: "quantize", Grid: 240}, true)
	elapsed := time.Since(started)
	if code != http.StatusOK || reply["unchanged"] == true || len(studioHistoryEdits(t, handler)) != 1 {
		t.Fatalf("500-note quantize: %d %v", code, reply)
	}
	t.Logf("500-note quantize through /api/pattern: %s", elapsed.Round(time.Millisecond))
}

// A capture may end in a write to the score; a note batch waits for it, as
// mixer and automation edits do.
func TestStudioNotesWaitForARunningCapture(t *testing.T) {
	isolateState(t)
	source, _ := notesStudioScore(4)
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	s.mu.Lock()
	s.captureID = "take-in-progress"
	s.mu.Unlock()
	handler := s.routes()
	code, reply := notesCall(t, handler, path, "take", &edit.EditNotes{Op: "quantize", Grid: 240}, true)
	if code != http.StatusConflict || reply["error"] != "Finish the take before editing notes." || string(notesRead(t, path)) != source || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatalf("a note batch during a capture: %d %v", code, reply)
	}
	s.mu.Lock()
	s.captureID = ""
	s.mu.Unlock()
	if code, reply := notesCall(t, handler, path, "take", &edit.EditNotes{Op: "quantize", Grid: 240}, true); code != http.StatusOK || reply["unchanged"] == true {
		t.Fatalf("after the capture: %d %v", code, reply)
	}
}
