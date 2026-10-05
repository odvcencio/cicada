package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
)

func TestGoSXLiveLeaseReleasesNotesAndRejectsLateInput(t *testing.T) {
	path := studioTestPath(t, studioScore)
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s := &studio{path: path, transport: &studioTransport{stream: stream, playing: true, audioNull: true}}
	h := s.routes()
	on, velocity, note := true, 100, 60
	request := studioLiveRequest{Owner: "test-mounted-engine", Sequence: 1, Messages: []audioClientMessage{{Type: "note", Track: "bass", Note: &note, Velocity: &velocity, On: &on}}}
	if r := studioCall(t, h, "/api/live", request); r.Code != 200 {
		t.Fatalf("note: %d %s", r.Code, r.Body.String())
	}
	if r := studioCall(t, h, "/api/live", request); r.Code != http.StatusConflict {
		t.Fatal("replayed sequence accepted")
	}
	if _, err := io.CopyN(io.Discard, stream, int64(liveBlockFrames*8)); err != nil {
		t.Fatal(err)
	}
	if r := studioCall(t, h, "/api/live", studioLiveRequest{Owner: request.Owner, Sequence: 3, Release: true}); r.Code != 200 {
		t.Fatalf("release: %d", r.Code)
	}
	request.Sequence = 4
	if r := studioCall(t, h, "/api/live", request); r.Code != http.StatusConflict {
		t.Fatal("late note-on resurrected disposed input")
	}
	if _, err := io.CopyN(io.Discard, stream, int64(liveBlockFrames*8)); err != nil {
		t.Fatal(err)
	}
	off := false
	for {
		select {
		case e := <-stream.Events():
			if e.Kind == "note-off" && e.Track == "bass" {
				off = true
			}
		default:
			if !off {
				t.Fatal("lease release did not release its held note")
			}
			return
		}
	}
}

func TestGoSXLiveReleasePreservesOtherMountsHeldPitch(t *testing.T) {
	path := studioTestPath(t, studioScore)
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s := &studio{path: path, transport: &studioTransport{stream: stream, playing: true, audioNull: true}}
	h := s.domainRoutes()
	on, velocity, note := true, 100, 60
	for _, owner := range []string{"first-mounted-engine", "second-mounted-engine"} {
		r := studioCall(t, h, "/api/live", studioLiveRequest{Owner: owner, Sequence: 1, Messages: []audioClientMessage{{Type: "note", Track: "bass", Note: &note, Velocity: &velocity, On: &on}}})
		if r.Code != 200 {
			t.Fatalf("note: %d %s", r.Code, r.Body.String())
		}
	}
	render := func() int {
		t.Helper()
		if _, err := io.CopyN(io.Discard, stream, int64(liveBlockFrames*8)); err != nil {
			t.Fatal(err)
		}
		offs := 0
		for {
			select {
			case e := <-stream.Events():
				if e.Kind == "note-off" && e.Track == "bass" {
					offs++
				}
			default:
				return offs
			}
		}
	}
	_ = render()
	for i, owner := range []string{"first-mounted-engine", "second-mounted-engine"} {
		r := studioCall(t, h, "/api/live", studioLiveRequest{Owner: owner, Sequence: 2, Release: true})
		if r.Code != 200 {
			t.Fatal(r.Code)
		}
		offs := render()
		if i == 0 && offs != 0 {
			t.Fatal("releasing one mount stopped another's held pitch")
		}
		if i == 1 && offs == 0 {
			t.Fatal("last owner did not release held pitch")
		}
	}
}

func TestGoSXDisposedLeasesDoNotConsumeActiveInputBudget(t *testing.T) {
	for _, closed := range []bool{true, false} {
		s := &studio{}
		s.liveControls.leases = map[string]*studioLiveLease{}
		for i := 0; i < 128; i++ {
			s.liveControls.leases[fmt.Sprintf("old-mounted-engine-%d", i)] = &studioLiveLease{Closed: closed, Seen: time.Now()}
		}
		r := studioCall(t, s.domainRoutes(), "/api/live", studioLiveRequest{Owner: "new-mounted-engine", Sequence: 1, Release: true})
		want := 429
		if closed {
			want = 200
		}
		if r.Code != want {
			t.Fatalf("closed=%v: got %d want %d", closed, r.Code, want)
		}
	}
}

func TestGoSXTakesProjectionKeepsPreparedSourceInJournal(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id := captureTestTake(t, s)
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	candidate := append(append([]byte(nil), source...), []byte("// retained prepared source\n")...)
	if err := s.takes.Prepare(id, source, candidate); err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, s.domainRoutes(), "/api/takes", nil)
	var view struct {
		Takes []map[string]any `json:"takes"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Takes) != 1 || view.Takes[0]["id"] != id {
		t.Fatal("missing take metadata")
	}
	if _, leaked := view.Takes[0]["candidate"]; leaked {
		t.Fatal("prepared source escaped the journal")
	}
	durable, err := s.takes.Get(id)
	if err != nil || string(durable.Candidate) != string(candidate) {
		t.Fatal("metadata projection changed the durable candidate")
	}
}

func TestGoSXProductionDomainHasNoBrowserHost(t *testing.T) {
	s := &studio{}
	for _, path := range []string{"/", "/studio-live.js", "/studio-midi.js", "/studio-capture.js", "/audio/cicada-client.js", "/audio/cicada-capture-client.js", "/api/audio/ws", "/api/kernel.wasm"} {
		r := studioCall(t, s.domainRoutes(), path, nil)
		if r.Code != 404 {
			t.Errorf("legacy production route %s: %d", path, r.Code)
		}
	}
}
