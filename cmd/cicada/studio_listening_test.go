package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/seq"
)

const studioListeningScore = `cicada 2
track left acid { level = -8dB }
track right acid { level = -10dB }
pattern pulse { 1 . 5 . }
arrange {
  place bed left pulse { at = @1.1.1 length = 4bars }
  place pickup right pulse { at = @2.2.1 length = 1/4 }
  marker later { at = @3.1.1 }
  marker opening { at = @1.1.1 }
  marker middle { at = @2.1.1 }
}
`

func listeningStudio(t *testing.T) *studio {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, []byte(studioListeningScore), 0600); err != nil {
		t.Fatal(err)
	}
	s := &studio{path: path, transport: newStudioTransport(path), history: newStudioHistory([]byte(studioListeningScore))}
	s.transport.audioNull, s.transport.history = true, s.history
	t.Cleanup(s.transport.close)
	return s
}

func listeningCall(t *testing.T, s *studio, input studioTransportInput) *httptest.ResponseRecorder {
	t.Helper()
	if input.Revision == "" {
		source, _ := os.ReadFile(s.path)
		input.Revision = studioRevision(source)
	}
	body, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	s.transportCommand(w, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewReader(body)))
	return w
}

func TestSessionListeningResolvesSectionsAndPreservesSongAuthority(t *testing.T) {
	s := listeningStudio(t)
	p, err := compileStudioSource(s.path, []byte(studioListeningScore))
	if err != nil {
		t.Fatal(err)
	}
	filtered, solo, err := prepareStudioListening(p, studioListening{Kind: "placement", Name: "pickup", Mode: "solo"})
	if err != nil || filtered != p || len(filtered.Arrange.Placements) != 2 || len(filtered.Tracks) != len(p.Tracks) {
		t.Fatalf("solo changed the canonical score or track identity: %v", err)
	}
	if solo.StartTick != 5*seq.PPQ || solo.EndTick != 6*seq.PPQ {
		t.Fatalf("pickup boundaries were rounded: %+v", solo)
	}
	context, _, err := prepareStudioListening(p, studioListening{Kind: "placement", Name: "pickup", Mode: "context"})
	if err != nil || context != p || len(context.Arrange.Placements) != 2 {
		t.Fatalf("context audition lost other placements: %v", err)
	}
	_, middle, err := prepareStudioListening(p, studioListening{Kind: "section", Name: "middle", Loop: true})
	if err != nil || middle.StartTick != seq.TicksPerBar || middle.EndTick != 2*seq.TicksPerBar {
		t.Fatalf("section did not find the next chronological marker: %v %+v", err, middle)
	}
	_, last, err := prepareStudioListening(p, studioListening{Kind: "section", Name: "later", Loop: true})
	if err != nil || last.EndTick != 4*seq.TicksPerBar {
		t.Fatalf("last section did not end with the arrangement: %v %+v", err, last)
	}
}

func TestSessionListeningLoopsStopsAndLeavesSourceAndUndoUnchanged(t *testing.T) {
	s := listeningStudio(t)
	response := listeningCall(t, s, studioTransportInput{Action: "loop", Start: "@2.2.1", End: "@2.2.2"})
	if response.Code != http.StatusOK {
		t.Fatalf("loop: %d %s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.transport.snapshot().Listening.Cycles < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	loop := s.transport.snapshot()
	if !loop.Playing || loop.Listening == nil || loop.Listening.Cycles < 2 || loop.Bar != 2 || !strings.Contains(loop.ListeningStatus, "Loop:") {
		t.Fatalf("native short loop: %+v", loop)
	}
	response = listeningCall(t, s, studioTransportInput{Action: "audition", Placement: "pickup", Mode: "solo"})
	if response.Code != http.StatusOK {
		t.Fatalf("audition: %d %s", response.Code, response.Body.String())
	}
	deadline = time.Now().Add(3 * time.Second)
	for s.transport.snapshot().Playing && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	state := s.transport.snapshot()
	if state.Playing || state.Listening == nil || !state.Listening.Completed || !strings.Contains(state.ListeningStatus, "Finished:") {
		t.Fatalf("audition did not stop at the selected end: %+v", state)
	}
	response = listeningCall(t, s, studioTransportInput{Action: "fullSong"})
	if response.Code != http.StatusOK || s.transport.snapshot().Listening != nil || !s.transport.snapshot().Playing {
		t.Fatalf("full song did not restore the complete score: %d %s", response.Code, response.Body.String())
	}
	source, _ := os.ReadFile(s.path)
	if string(source) != studioListeningScore || len(s.history.historySnapshot().Edits) != 0 {
		t.Fatal("listening wrote source or added an Undo edit")
	}
	_ = listeningCall(t, s, studioTransportInput{Action: "stop"})
}

func TestSessionListeningRejectsStaleInvalidAndDeletedTargets(t *testing.T) {
	s := listeningStudio(t)
	for _, input := range []studioTransportInput{
		{Action: "loop", Start: "@1.1.1", End: "@1.1.1"},
		{Action: "loop", Start: "@1.1.1", End: "@6.1.1"},
		{Action: "loopSection", Section: "missing"},
		{Action: "audition", Placement: "missing"},
		{Action: "audition", Placement: "pickup", Mode: "invalid"},
	} {
		if response := listeningCall(t, s, input); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid target accepted: %+v %d %s", input, response.Code, response.Body.String())
		}
	}
	if response := listeningCall(t, s, studioTransportInput{Action: "loopSection", Section: "opening", Revision: "old"}); response.Code != http.StatusConflict {
		t.Fatalf("stale listening request accepted: %d", response.Code)
	}
	if response := listeningCall(t, s, studioTransportInput{Action: "audition", Placement: "pickup", Loop: true}); response.Code != http.StatusOK {
		t.Fatalf("solo loop: %d %s", response.Code, response.Body.String())
	}
	updated := strings.Replace(studioListeningScore, "  place pickup right pulse { at = @2.2.1 length = 1/4 }\n", "", 1)
	if err := os.WriteFile(s.path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	s.transport.poll()
	state := s.transport.snapshot()
	if state.Playing || state.Listening != nil || !strings.Contains(state.Error, "no longer") {
		t.Fatalf("deleted audition target kept playing stale audio: %+v", state)
	}
}
