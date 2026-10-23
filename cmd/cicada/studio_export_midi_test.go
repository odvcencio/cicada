package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/smf"
)

func TestStudioMIDIExportRetainsEarlierDeliveryAndWAV(t *testing.T) {
	path := copyStudioProject(t)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	handler := s.routes()
	s.exports.status = studioExportStatus{ID: "accepted-wav", State: "succeeded", Path: "accepted.wav"}
	readStatus := func() studioMIDIExportStatus {
		t.Helper()
		var status studioMIDIExportStatus
		if err := json.Unmarshal(studioCall(t, handler, "/api/export/midi", nil).Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	if readStatus().State != "idle" {
		t.Fatal("new project invented a MIDI export")
	}
	seen, err := project.ReadScoreRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]string{"source_revision": seen}
	response := studioCall(t, handler, "/api/export/midi", request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	first := readStatus()
	if first.ID == "" || first.Notes < 1 || first.PPQ != 960 || first.State != "ready" || first.SourceRevision != seen || first.CurrentSourceRevision != seen {
		t.Fatal("invalid completed MIDI projection", first)
	}
	download := studioCall(t, handler, "/api/export/midi/file/"+first.ID, nil)
	if download.Code != 200 || download.Header().Get("Content-Type") != "audio/midi" || !bytes.HasPrefix(download.Body.Bytes(), []byte("MThd")) {
		t.Fatal("MIDI was not downloadable", download.Code, download.Body.String())
	}
	if _, err := smf.Decode(bytes.NewReader(download.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	s.midiExport.mu.Lock()
	s.midiExport.active = true
	s.midiExport.mu.Unlock()
	if busy := studioCall(t, handler, "/api/export/midi", request); busy.Code != 409 {
		t.Fatal("concurrent generation was accepted", busy.Code)
	}
	if old := studioCall(t, handler, "/api/export/midi/file/"+first.ID, nil); old.Code != 200 || !bytes.Equal(old.Body.Bytes(), download.Body.Bytes()) {
		t.Fatal("preparing another MIDI hid the completed one")
	}
	s.midiExport.mu.Lock()
	s.midiExport.active = false
	s.midiExport.mu.Unlock()
	part := filepath.Join(filepath.Dir(path), "parts/patterns.cicada")
	original, err := os.ReadFile(part)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, append(bytes.Clone(original), []byte("\n// saved by another editor\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if stale := studioCall(t, handler, "/api/export/midi", request); stale.Code != 409 || readStatus().ID != first.ID {
		t.Fatal("unseen secondary source replaced the earlier MIDI", stale.Code)
	}
	updated, err := project.ReadScoreRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	request["source_revision"] = updated
	if fresh := studioCall(t, handler, "/api/export/midi", request); fresh.Code != 200 || readStatus().ID == first.ID {
		t.Fatal("a reviewed source cannot export", fresh.Code)
	}
	current := readStatus()
	if old := studioCall(t, handler, "/api/export/midi/file/"+first.ID, nil); old.Code != 409 {
		t.Fatal("an old ID downloaded different notes")
	}
	if err := os.WriteFile(part, append(bytes.Clone(original), []byte("\npattern incomplete {\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	request["source_revision"], _ = project.ReadScoreRevision(path)
	if invalid := studioCall(t, handler, "/api/export/midi", request); invalid.Code != 422 || readStatus().ID != current.ID {
		t.Fatal("invalid source replaced a completed MIDI", invalid.Code)
	}
	if old := studioCall(t, handler, "/api/export/midi/file/"+current.ID, nil); old.Code != 200 || !bytes.Equal(old.Body.Bytes(), download.Body.Bytes()) {
		t.Fatal("rejected export altered the prior MIDI bytes")
	}
	if s.exports.snapshot().ID != "accepted-wav" || s.exports.snapshot().Path != "accepted.wav" {
		t.Fatal("MIDI export replaced the completed WAV")
	}
}

func TestStudioMIDIExportIncludesRecordedProjects(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id := captureTestTake(t, s)
	source, _ := os.ReadFile(s.path)
	if err := s.commitTake(id, studioRevision(source), nil); err != nil {
		t.Fatal(err)
	}
	revision, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, s.routes(), "/api/export/midi", map[string]string{"source_revision": revision})
	var status studioMIDIExportStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || response.Code != 200 || status.Notes == 0 || len(status.OmittedAudio) != 1 || status.OmittedAudio[0] != "vox" {
		t.Fatal("recording prevented MIDI export", response.Code, response.Body.String(), err)
	}
}

func TestStudioMIDIExportRejectsUnsafeRequests(t *testing.T) {
	handler, _ := studioTestHandler(t)
	for _, input := range []struct {
		body, origin, contentType string
		want                      int
	}{
		{`{}`, "http://127.0.0.1:1234", "application/json", 400},
		{`{"source_revision":"x","path":"escape.mid"}`, "http://127.0.0.1:1234", "application/json", 400},
		{`{"source_revision":"x"} {}`, "http://127.0.0.1:1234", "application/json", 400},
		{`{"source_revision":"x"}`, "https://outside.example", "application/json", 403},
		{`{}`, "http://127.0.0.1:1234", "text/plain", 415},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/export/midi", bytes.NewBufferString(input.body))
		r.Header.Set("Content-Type", input.contentType)
		r.Header.Set("Origin", input.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != input.want {
			t.Fatal("unsafe MIDI request accepted", input.body, w.Code, w.Body.String())
		}
	}
	if missing := studioCall(t, handler, "/api/export/midi/file/unknown", nil); missing.Code != http.StatusConflict {
		t.Fatal("arbitrary ID downloaded a file", missing.Code)
	}
}
