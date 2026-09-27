package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

func TestStudioNoteMessagesValidateAndReachLiveplay(t *testing.T) {
	path := studioTestPath(t, studioScore)
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s := &studio{path: path, transport: &studioTransport{stream: stream, playing: true, audioNull: true}}
	for _, raw := range []string{
		`{"type":"note","track":"bass","note":60,"velocity":90,"on":true}`,
		`{"type":"note","track":"drums","note":36,"velocity":110,"on":true}`,
	} {
		var message audioClientMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		if err := s.applyAudioMessage(message); err != nil {
			t.Fatalf("apply %s: %v", raw, err)
		}
	}
	if _, err := io.CopyN(io.Discard, stream, int64(liveBlockFrames*8)); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for {
		select {
		case event := <-stream.Events():
			if event.Kind == "note-on" {
				seen[event.Track] = true
			}
		default:
			goto noteOnsDrained
		}
	}
noteOnsDrained:
	if !seen["bass"] || !seen["drums"] {
		t.Fatalf("audio note messages did not reach liveplay: %+v", seen)
	}
	for _, raw := range []string{
		`{"type":"note","track":"bass","note":60,"velocity":0,"on":false}`,
		`{"type":"note","track":"drums","note":36,"velocity":0,"on":false}`,
	} {
		var message audioClientMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		if err := s.applyAudioMessage(message); err != nil {
			t.Fatalf("apply %s: %v", raw, err)
		}
	}
	invalid := []string{
		`{"type":"note","track":"bass","note":60,"velocity":90}`,
		`{"type":"note","track":"bass","note":-1,"velocity":90,"on":true}`,
		`{"type":"note","track":"bass","note":128,"velocity":90,"on":true}`,
		`{"type":"note","track":"bass","note":60,"velocity":-1,"on":true}`,
		`{"type":"note","track":"bass","note":60,"velocity":128,"on":true}`,
		`{"type":"note","track":"missing","note":60,"velocity":90,"on":true}`,
		`{"type":"note","track":"drums","note":60,"velocity":90,"on":true}`,
	}
	for _, raw := range invalid {
		var message audioClientMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		if err := s.applyAudioMessage(message); err == nil {
			t.Errorf("accepted invalid note message %s", raw)
		} else if response := audioErrorResponse(message, err); response.Type != "error" || response.Code != "CICADA-NOTE" || response.Address != message.Track {
			t.Errorf("invalid note was not returned as CICADA-NOTE: %+v", response)
		}
	}
	if _, err := io.CopyN(io.Discard, stream, int64(liveBlockFrames*8)); err != nil {
		t.Fatalf("invalid note messages faulted liveplay: %v", err)
	}
}

func TestRecordedTakePatchesAcidSlidesAndDrumVelocity(t *testing.T) {
	acid, err := recordedTakeSource([]byte(studioScore), "bass", "pulse", []studioTakeNote{
		{Tick: 0, EndTick: 180, Note: 60, Velocity: 90},
		{Tick: 120, EndTick: 240, Note: 64, Velocity: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(acid), "pattern pulse acid steps=4 { c4~ e4 5 . }") {
		t.Fatalf("acid capture did not patch pitch and overlapping slide: %s", acid)
	}
	drums, err := recordedTakeSource([]byte(studioScore), "drums", "beat", []studioTakeNote{
		{Tick: 120, EndTick: 120, Note: 36, Velocity: 80},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(drums), "bd: xx6..") {
		t.Fatalf("drum capture did not patch hit and velocity: %s", drums)
	}
}

func TestRecordTakeCreatesOneRevisionAndHistoryEntry(t *testing.T) {
	path := studioTestPath(t, studioScore)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s.transport.stream = stream
	handler := s.routes()
	take := studioEdit{
		Revision: studioRevision([]byte(studioScore)),
		Recordings: []studioTakeRecording{{Track: "bass", Pattern: "pulse", Notes: []studioTakeNote{
			{Tick: 0, EndTick: 100, Note: 60, Velocity: 96},
			{Tick: 240, EndTick: 340, Note: 64, Velocity: 96},
		}}},
	}
	response := studioCall(t, handler, "/api/record", take)
	if response.Code != 200 {
		t.Fatalf("record commit: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision == take.Revision {
		t.Fatal("take commit did not create a source revision")
	}
	s.transport.poll()
	events := studioHistoryEvents(t, handler)
	if len(events) != 2 || events[0].Detail != "Recorded 2 notes into pulse" {
		t.Fatalf("take commit did not create one history entry: %+v", events)
	}
	committed, err := os.ReadFile(path)
	if err != nil || studioRevision(committed) != result.Revision {
		t.Fatalf("source revision and commit response differ: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func studioTestPath(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
