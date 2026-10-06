package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"m31labs.dev/cicada/host/transcription"
	"m31labs.dev/cicada/project"
)

func TestTranscriptionPreviewIsPrivateAndRevisionChecked(t *testing.T) {
	options := transcription.DefaultOptions()
	options.Tempo = 120
	result, err := transcription.Complete(transcription.Result{Notes: []transcription.Note{{Start: 0, End: .4, MIDI: 60, PitchHz: 261.63, Confidence: .9, Velocity: 100}}}, options)
	if err != nil {
		t.Fatal(err)
	}
	var revision atomic.Value
	revision.Store("initial")
	commands := make(chan map[string]any, 4)
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: revision.Load().(string), Source: "title \"Existing\"", Valid: true, Project: &project.Project{Title: "Existing", Edition: 2}})
		case "/api/transport":
			_, _ = io.WriteString(w, `{}`)
		case "/api/takes":
			_, _ = io.WriteString(w, `{"takes":[{"id":"retained","frames":32000,"rate":16000}]}`)
		case "/api/transcribe":
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				defer r.MultipartForm.RemoveAll()
				if len(r.MultipartForm.File["wav"]) != 1 || r.FormValue("grid") != "16" {
					t.Error("upload fields lost")
				}
			} else {
				var request struct {
					TakeID string `json:"takeId"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.TakeID != "retained" {
					t.Error("wrong completed take")
				}
			}
			_ = json.NewEncoder(w).Encode(result)
		case "/api/source":
			var command map[string]any
			_ = json.NewDecoder(r.Body).Decode(&command)
			commands <- command
			if command["revision"] != revision.Load() {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"score changed"}`)
				return
			}
			_, _ = io.WriteString(w, `{"revision":"saved","valid":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
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
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=takes"))
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"csrf_token": csrf, "revision": "initial", "grid": "16", "tempo": "120", actionReturn: "/?panel=takes"} {
		_ = form.WriteField(key, value)
	}
	file, _ := form.CreateFormFile("wav", "melody.wav")
	_, _ = file.Write([]byte("WAV fixture"))
	_ = form.Close()
	req, _ := http.NewRequest(http.MethodPost, app.URL+"/__actions/transcribe", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Origin", app.URL)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || len(commands) != 0 {
		t.Fatalf("preview mutated source: %d", response.StatusCode)
	}
	page := getPage(t, client, app.URL+"/?panel=takes")
	if !strings.Contains(page, "Melody preview") || !strings.Contains(page, "Replace score") || !strings.Contains(page, "pitch confidence 90%") {
		t.Fatal("missing preview controls")
	}
	preview, err := client.Get(app.URL + "/media/transcription")
	if err != nil {
		t.Fatal(err)
	}
	wav, _ := io.ReadAll(preview.Body)
	preview.Body.Close()
	if preview.StatusCode != http.StatusOK || preview.Header.Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(wav, []byte("RIFF")) {
		t.Fatalf("score audition unavailable: %d %s", preview.StatusCode, wav)
	}
	otherJar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: otherJar}
	if strings.Contains(getPage(t, other, app.URL+"/?panel=takes"), "Melody preview") {
		t.Fatal("preview leaked to another session")
	}
	preview, err = other.Get(app.URL + "/media/transcription")
	if err != nil {
		t.Fatal(err)
	}
	preview.Body.Close()
	if preview.StatusCode != http.StatusNotFound {
		t.Fatal("audio preview leaked")
	}
	revision.Store("changed")
	fields := url.Values{"csrf_token": {csrf}, "revision": {"changed"}, actionReturn: {"/?panel=takes"}}
	response = post(t, client, app.URL+"/__actions/transcription-apply", fields, true, app.URL)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale preview applied: %d", response.StatusCode)
	}
	command := <-commands
	if command["revision"] != "initial" || command["source"] != result.Source {
		t.Fatal("preview revision or source lost")
	}
	revision.Store("initial")
	response = post(t, client, app.URL+"/__actions/transcription-apply", fields, false, app.URL)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply failed: %d", response.StatusCode)
	}
	<-commands
	if strings.Contains(getPage(t, client, app.URL+"/?panel=takes"), `id="transcription-preview"`) {
		t.Fatal("applied preview not cleared")
	}
	fields.Set("takeId", "retained")
	fields.Set("grid", "16")
	response = post(t, client, app.URL+"/__actions/transcribe", fields, false, app.URL)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("completed take transcription failed: %d", response.StatusCode)
	}
	draftFields := url.Values{"csrf_token": {csrf}, "revision": {"stale"}, "content": {"Existing score draft"}, actionReturn: {"/?panel=code"}}
	response = post(t, client, app.URL+"/__actions/source", draftFields, true, app.URL)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("draft setup failed: %d", response.StatusCode)
	}
	<-commands
	response = post(t, client, app.URL+"/__actions/transcription-apply", fields, true, app.URL)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity || len(commands) != 0 {
		t.Fatal("transcription replaced an unsaved score draft")
	}
	if !strings.Contains(getPage(t, client, app.URL+"/?panel=code"), "Existing score draft") {
		t.Fatal("transcription discarded the score draft")
	}
	response = post(t, client, app.URL+"/__actions/transcription-discard", fields, false, app.URL)
	response.Body.Close()
	if strings.Contains(getPage(t, client, app.URL+"/?panel=takes"), `id="transcription-preview"`) {
		t.Fatal("discard did not clear preview")
	}
}

const actionReturn = "__gosx_return_to"
