package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMIDIExportActionUsesReviewedSourcesWithoutNavigating(t *testing.T) {
	address, client, edits := testApp(t)
	page := getPage(t, client, address+"/studio")
	if !strings.Contains(page, "Notes and timing (MIDI)") || !strings.Contains(page, "/__actions/export-midi") {
		t.Fatal("MIDI controls are missing from Export")
	}
	csrf := tokenFromPage(t, page)
	fields := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"current-sources"}, "__gosx_return_to": {"/studio"}}
	response := post(t, client, address+"/__actions/export-midi", fields, true, address)
	defer response.Body.Close()
	var result struct {
		OK       bool   `json:"ok"`
		Message  string `json:"message"`
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != http.StatusOK || !result.OK || result.Redirect != "" || result.Message != "MIDI is ready to download." || len(*edits) != 1 || (*edits)[0]["source_revision"] != "current-sources" {
		t.Fatal("MIDI export navigated or lost its loaded sources", response.StatusCode, result, err)
	}
}

func TestMIDIExportLabelsKeepAnEarlierFileAvailable(t *testing.T) {
	for _, current := range []string{"saved", "edited", ""} {
		view := midiExportView{ID: "notes", State: "ready", SourceRevision: "saved", CurrentSourceRevision: current, Notes: 52, PPQ: 960, OmittedAudio: []string{"guitar"}}
		view.project()
		if view.DownloadHidden || view.DownloadURL != "/media/midi-exports/notes.mid" || !strings.Contains(view.Progress, "52 notes") || !strings.Contains(view.Progress, "Audio omitted from guitar") {
			t.Fatal("completed MIDI is unavailable or implies it includes audio", view)
		}
		if current == "edited" && view.DownloadLabel != "Download earlier MIDI" || current == "" && !strings.Contains(view.DownloadLabel, "unverified") {
			t.Fatal("MIDI mislabeled a different or unknown saved score", view)
		}
	}
}
