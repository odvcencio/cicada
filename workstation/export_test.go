package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestExportActionsPassTheDisplayedScoreRevision(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "target_lufs": {"-16"}, "true_peak_max": {"-1"}, "tolerance": {"1"}, "rate": {"48000"}, "bits": {"24"}}
	for index, managed := range []bool{false, true} {
		response := post(t, client, address+"/__actions/export", form, managed, address)
		want := http.StatusSeeOther
		if managed {
			want = http.StatusOK
		}
		if response.StatusCode != want || len(*edits) != index+1 || (*edits)[index]["revision"] != "current" {
			t.Fatal("export omitted the displayed source revision or used the wrong response contract", managed, response.StatusCode)
		}
	}
}

func TestEnhancedExportAcknowledgesWithoutNavigating(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio"))
	fields := map[string]string{"csrf_token": csrf, "revision": "current", "target_lufs": "-16", "true_peak_max": "-1", "tolerance": "1", "rate": "48000", "bits": "24", "__gosx_return_to": "/studio"}
	body, _ := json.Marshal(fields)
	request, err := http.NewRequest(http.MethodPost, address+"/__actions/export", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		OK       bool   `json:"ok"`
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !result.OK || result.Redirect != "" || response.Header.Get("Location") != "" || len(*edits) != 1 || (*edits)[0]["revision"] != "current" {
		t.Fatal("export redirected or lost its saved revision", response.StatusCode, result, *edits)
	}
}

func TestExportVersionLabelsFollowTheSavedSong(t *testing.T) {
	for _, test := range []struct {
		name, state, revision, current, message, label string
	}{
		{"current", "succeeded", "saved", "saved", "matches the saved score", "Download WAV"},
		{"rendering", "rendering", "saved", "saved", "Rendering the saved score", "Download WAV"},
		{"edited", "succeeded", "rendered", "edited", "score has changed", "Download earlier render"},
		{"edited while rendering", "rendering", "rendered", "edited", "after this job finishes", "Download earlier render"},
		{"earlier shortfall", "shortfall", "rendered", "edited", "score has changed", "Download earlier shortfall WAV"},
		{"missing song", "succeeded", "rendered", "", "cannot be checked", "Download WAV (version unverified)"},
		{"unknown render", "succeeded", "", "saved", "cannot be checked", "Download WAV (version unverified)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := exportView{ID: "job", State: test.state, Revision: test.revision, SourceRevision: test.revision, CurrentSourceRevision: test.current}
			view.project()
			if !strings.Contains(view.Version, test.message) || view.DownloadLabel != test.label {
				t.Fatalf("version message or download label: %+v", view)
			}
			completed := test.state == "succeeded" || test.state == "shortfall"
			if completed != !view.DownloadHidden || completed && view.DownloadURL != "/media/exports/job.wav" {
				t.Fatalf("version labels changed render availability: %+v", view)
			}
		})
	}
	idle := exportView{State: "idle"}
	idle.project()
	if idle.Version != "" || !idle.DownloadHidden {
		t.Fatal("idle export implied a completed render", idle)
	}
}

func TestExportProjectionAndDownloadStayOnThePrivateBackend(t *testing.T) {
	t.Setenv("CICADA_BACKEND_TOKEN", "private-render-token")
	completed := true
	wav := "RIFFtestWAVEprivate-audio"
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-render-token" {
			t.Error("export lost the private service credential")
		}
		switch r.URL.Path {
		case "/api/export":
			w.Header().Set("Content-Type", "application/json")
			if completed {
				_, _ = io.WriteString(w, `{"id":"render-1","state":"shortfall","report":{"target_lufs":-14,"achieved_lufs":-16.2,"true_peak_dbtp":-1.1}}`)
			} else {
				_, _ = io.WriteString(w, `{"id":"render-2","state":"rendering","pass":2,"pass_limit":6}`)
			}
		case "/api/export/file/render-1":
			w.Header().Set("Content-Type", "audio/wav")
			w.Header().Set("Content-Disposition", `attachment; filename="finished.wav"`)
			_, _ = io.WriteString(w, wav)
		default:
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `{"error":"render unavailable"}`)
		}
	}))
	defer audio.Close()
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest("GET", "http://127.0.0.1/api/export", nil))
	var projection exportView
	if err := json.Unmarshal(status.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.DownloadHidden || projection.DownloadURL != "/media/exports/render-1.wav" || !strings.Contains(projection.Quality, "not reached") || !strings.Contains(projection.Quality, "-16.2 LUFS") {
		t.Fatalf("shortfall projection: %+v", projection)
	}
	file := httptest.NewRecorder()
	handler.ServeHTTP(file, httptest.NewRequest("GET", "http://127.0.0.1"+projection.DownloadURL, nil))
	if file.Code != 200 || file.Body.String() != wav || file.Header().Get("Cache-Control") != "no-store" || !strings.Contains(file.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %s", file.Code, file.Body.String())
	}
	completed = false
	status = httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest("GET", "http://127.0.0.1/api/export", nil))
	if err := json.Unmarshal(status.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if !projection.DownloadHidden || !projection.Busy || projection.DownloadURL != "" {
		t.Fatal("unfinished render offered a stale download")
	}
	if strings.Contains(status.Body.String(), "private-render-token") || strings.Contains(file.Body.String(), "private-render-token") {
		t.Fatal("private export token leaked into the browser")
	}
}
