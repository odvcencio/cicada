package main

import (
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
	response := post(t, client, address+"/__actions/export", form, true, address)
	if response.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["revision"] != "current" {
		t.Fatal("export omitted the displayed source revision")
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
