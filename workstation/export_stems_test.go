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

func TestStemsExportActionPreservesSourcesAndNavigation(t *testing.T) {
	for _, managed := range []bool{false, true} {
		address, client, edits := testApp(t)
		page := getPage(t, client, address+"/studio")
		if !strings.Contains(page, "Individual tracks (stems)") || !strings.Contains(page, "stems-export-result") {
			t.Fatal("stems are absent from Export")
		}
		fields := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"current-sources"}, "rate": {"96000"}, "__gosx_return_to": {"/studio"}}
		response := post(t, client, address+"/__actions/export-stems", fields, managed, address)
		defer response.Body.Close()
		want := http.StatusSeeOther
		if managed {
			want = http.StatusOK
			var result struct {
				OK       bool   `json:"ok"`
				Redirect string `json:"redirect"`
				Message  string `json:"message"`
			}
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil || !result.OK || result.Redirect != "" || result.Message != "Stem rendering started." {
				t.Fatal("background stems navigated or lost acknowledgement", result, err)
			}
		}
		if response.StatusCode != want || len(*edits) != 1 || (*edits)[0]["source_revision"] != "current-sources" || (*edits)[0]["rate"] != float64(96000) {
			t.Fatal("stems lost the reviewed source or sample rate", response.StatusCode, *edits)
		}
		for _, invalid := range []url.Values{
			{"csrf_token": fields["csrf_token"], "revision": {"current"}, "rate": {"48000"}},
			{"csrf_token": fields["csrf_token"], "revision": {"current"}, "source_revision": {"current-sources"}, "rate": {"NaN"}},
			{"csrf_token": fields["csrf_token"], "revision": {"current"}, "source_revision": {"current-sources"}, "rate": {"8000"}},
		} {
			rejected := post(t, client, address+"/__actions/export-stems", invalid, true, address)
			rejected.Body.Close()
			if rejected.StatusCode != http.StatusUnprocessableEntity || len(*edits) != 1 {
				t.Fatal("invalid stems request reached the render service", rejected.StatusCode, *edits)
			}
		}
	}
}

func TestStemsExportRetainsAndLabelsTheLastCompletedDownload(t *testing.T) {
	for _, state := range []string{"succeeded", "queued", "rendering", "packaging", "failed"} {
		for _, current := range []string{"saved", "edited", ""} {
			view := stemsExportView{ID: "complete", SourceRevision: "saved", CurrentSourceRevision: current, JobSourceRevision: "saved", State: state, Tracks: 4, Rate: 48000}
			view.project()
			if view.DownloadHidden || view.DownloadURL != "/media/stem-exports/complete.zip" || !strings.Contains(view.Delivery, "4 tracks") || !strings.Contains(view.Delivery, "float32") {
				t.Fatal("last completed archive lost or format mislabeled", view)
			}
			if view.Busy != (state == "queued" || state == "rendering" || state == "packaging") {
				t.Fatal("incorrect render availability", view)
			}
			if current == "edited" && view.DownloadLabel != "Download earlier stems ZIP" || current == "" && !strings.Contains(view.DownloadLabel, "unverified") {
				t.Fatal("archive source identity mislabeled", view)
			}
			if view.Busy && current == "edited" && !strings.Contains(view.Progress, "earlier saved version") {
				t.Fatal("active render silently excludes a newer source", view)
			}
		}
	}
	view := stemsExportView{State: "failed", Error: "render failed"}
	view.project()
	if !view.DownloadHidden || view.DownloadURL != "" || view.Version != "" {
		t.Fatal("failed first render exposes a nonexistent archive", view)
	}
}

func TestStemsExportStreamsOnlyThePrivateCompletedArchive(t *testing.T) {
	t.Setenv("CICADA_BACKEND_TOKEN", "private-stems-test-token")
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-stems-test-token" {
			t.Error("private render credential omitted")
		}
		if r.URL.Path == "/api/export/stems" {
			_, _ = io.WriteString(w, `{"id":"ready","state":"packaging","source_revision":"saved","current_source_revision":"saved","tracks":2,"rate":48000}`)
			return
		}
		if r.URL.Path != "/api/export/stems/file/ready" || r.URL.RawQuery != "" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="song-stems.zip"`)
		_, _ = io.WriteString(w, "PK-private-archive")
	}))
	defer service.Close()
	b, err := newBackend(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	s := &studioApp{backend: b}
	request := httptest.NewRequest(http.MethodGet, "/media/stem-exports/ready.zip?path=/private/other", nil)
	request.SetPathValue("id", "ready.zip")
	response := httptest.NewRecorder()
	s.exportStems(response, request)
	if response.Code != 200 || response.Body.String() != "PK-private-archive" || response.Header().Get("Content-Type") != "application/zip" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private ZIP delivery failed", response.Code, response.Header(), response.Body.String())
	}
	status := httptest.NewRecorder()
	s.stemsExportStatus(status, httptest.NewRequest("GET", "/api/export/stems", nil))
	var view stemsExportView
	if err := json.Unmarshal(status.Body.Bytes(), &view); err != nil || view.DownloadHidden || !view.Busy || status.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("last successful ZIP disappeared while packaging", view, err)
	}
}
