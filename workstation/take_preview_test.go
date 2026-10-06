package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestTakeAuditionUsesPrivateNativeVoiceAndRejectsInvalidNotes(t *testing.T) {
	t.Setenv("CICADA_BACKEND_TOKEN", "audition-private")
	commands := make(chan map[string]any, 4)
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer audition-private" {
			t.Error("native credential missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "rev", Project: &project.Project{Title: "Capture", Edition:2, Tracks: []project.Track{{ID: "input", Kind: "audio"}, {ID: "bass", Kind: "acid"}}}})
		case "/api/transport":
			_, _ = io.WriteString(w, `{}`)
		case "/api/takes":
			if r.Method == "GET" {
				_, _ = io.WriteString(w, `{"takes":[{"id":"retained","track":"input","frames":48000,"rate":48000,"stage":"committed","asset":{"path":"assets/private.wav"}}]}`)
				return
			}
			var command map[string]any
			_ = json.NewDecoder(r.Body).Decode(&command)
			commands <- command
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = io.WriteString(w, "RIFF-private-voice")
		default:
			w.WriteHeader(404)
		}
	}))
	defer audio.Close()
	b, _ := newBackend(audio.URL)
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	response, err := http.Get(app.URL + takePreviewURL("retained", "rev", 72, 60, true))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "audio/wav" || response.Header.Get("Cache-Control") != "no-store" || string(data) != "RIFF-private-voice" {
		t.Fatalf("invalid audition response: %d %q", response.StatusCode, data)
	}
	command := <-commands
	if command["action"] != "audition" || command["takeId"] != "retained" || command["revision"] != "rev" {
		t.Fatalf("incorrect native command: %#v", command)
	}
	sample := command["sample"].(map[string]any)
	if sample["note"] != float64(72) || sample["root"] != float64(60) || sample["loop"] != true {
		t.Fatalf("incorrect sample voice: %#v", sample)
	}
	response, err = http.Get(app.URL + "/media/takes/retained.wav?note=128&root=60&revision=rev")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 || len(commands) != 0 {
		t.Fatal("invalid note reached the native service")
	}
	page := getPage(t, http.DefaultClient, app.URL+"/?panel=takes&take=retained&note=72&root=60")
	if !strings.Contains(page, "<audio") || !strings.Contains(page, "Sample audition") || !strings.Contains(page, `value="input"`) || strings.Contains(page, `value="bass"`) || strings.Contains(page, "assets/private.wav") || strings.Contains(page, "audition-private") {
		t.Fatal("audition page lacks controls or leaked private paths")
	}
}
