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

	"golang.org/x/net/html"
)

func TestExportActionsPassTheDisplayedScoreRevision(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"current-sources"}, "target_lufs": {"-16"}, "true_peak_max": {"-1"}, "tolerance": {"1"}, "rate": {"48000"}, "bits": {"24"}}
	for index, managed := range []bool{false, true} {
		response := post(t, client, address+"/__actions/export", form, managed, address)
		want := http.StatusSeeOther
		if managed {
			want = http.StatusOK
		}
		if response.StatusCode != want || len(*edits) != index+1 || (*edits)[index]["revision"] != "current" || (*edits)[index]["source_revision"] != "current-sources" {
			t.Fatal("export omitted the displayed source revision or used the wrong response contract", managed, response.StatusCode)
		}
	}
}

func TestEnhancedExportAcknowledgesWithoutNavigating(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio"))
	fields := map[string]string{"csrf_token": csrf, "revision": "current", "source_revision": "current-sources", "target_lufs": "-16", "true_peak_max": "-1", "tolerance": "1", "rate": "48000", "bits": "24", "__gosx_return_to": "/studio"}
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
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !result.OK || result.Redirect != "" || result.Message != "Rendering started." || response.Header.Get("Location") != "" || len(*edits) != 1 || (*edits)[0]["revision"] != "current" || (*edits)[0]["source_revision"] != "current-sources" {
		t.Fatal("export redirected or lost its saved revision", response.StatusCode, result, *edits)
	}
}

func TestExportRequiresTheLoadedSourceSnapshot(t *testing.T) {
	address, client, edits := testApp(t)
	for _, path := range []string{"/studio", "/?panel=export"} {
		page := getPage(t, client, address+path)
		if !strings.Contains(page, `name="source_revision" value="current-sources"`) {
			t.Fatal("export form omitted its complete source identity", path)
		}
	}
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio"))
	response := post(t, client, address+"/__actions/export", url.Values{"csrf_token": {csrf}, "revision": {"current"}}, true, address)
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusUnprocessableEntity || len(*edits) != 0 || !strings.Contains(string(data), "Refresh Studio") {
		t.Fatal("an export without a loaded source snapshot reached the audio service", response.StatusCode, string(data), *edits)
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

func TestExportProjectionKeepsTheFocusedFormOutsideTheLiveRegion(t *testing.T) {
	address, client, _ := testApp(t)
	document, err := html.Parse(strings.NewReader(getPage(t, client, address+"/?panel=export")))
	if err != nil {
		t.Fatal(err)
	}
	var result, button *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for _, attr := range node.Attr {
			if attr.Key == "id" {
				if attr.Val == "export-result" {
					result = node
				}
				if attr.Val == "export-render-button" {
					button = node
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	if result == nil || button == nil {
		t.Fatal("export result or render button missing")
	}
	for node := button; node != nil; node = node.Parent {
		if node == result {
			t.Fatal("focused render button would suspend the live region")
		}
	}
	var checkReadOnly func(*html.Node)
	checkReadOnly = func(node *html.Node) {
		if node.Data == "form" || node.Data == "input" || node.Data == "select" {
			t.Fatal("editable field is inside the read-only export live region")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			checkReadOnly(child)
		}
	}
	checkReadOnly(result)
}

func TestStudioFaviconProbeDoesNotReportAMissingResource(t *testing.T) {
	handler, err := newApp(nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "http://127.0.0.1/favicon.ico", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("favicon probe status = %d", recorder.Code)
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
				_, _ = io.WriteString(w, `{"id":"render-1","state":"shortfall","path":"/srv/private/exports/finished.wav","report":{"target_lufs":-14,"achieved_lufs":-16.2,"true_peak_dbtp":-1.1}}`)
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
	if projection.FileName != "finished.wav" || projection.Path != "" || strings.Contains(status.Body.String(), "/srv/private") || strings.Contains(status.Body.String(), `"path"`) {
		t.Fatalf("export exposed a local directory: %s", status.Body.String())
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

func TestExportProgressExplainsRenderStates(t *testing.T) {
	for _, tc := range []struct {
		state, want string
		pass, limit int
	}{
		{"", "Ready to render", 0, 0},
		{"idle", "Ready to render", 0, 0},
		{"queued", "Render queued", 0, 0},
		{"rendering", "Rendering WAV", 0, 6},
		{"rendering", "Rendering · pass 2 of 6", 2, 6},
		{"succeeded", "Render complete · WAV ready", 0, 0},
		{"shortfall", "Render complete · loudness target not reached", 0, 0},
		{"failed", "Render failed", 0, 0},
	} {
		t.Run(tc.state+tc.want, func(t *testing.T) {
			v := exportView{State: tc.state, Pass: tc.pass, PassLimit: tc.limit}
			v.project()
			if v.Progress != tc.want {
				t.Fatalf("progress = %q, want %q", v.Progress, tc.want)
			}
		})
	}
}

func TestExportFileNameDoesNotExposeUnixOrWindowsDirectories(t *testing.T) {
	for _, filename := range []string{"/srv/private/music/finished.wav", `Z:\Private\Music\finished.wav`} {
		v := exportView{Path: filename, State: "succeeded", ID: "render-1"}
		v.project()
		if v.FileName != "finished.wav" || v.Path != "" {
			t.Fatalf("projection: %+v", v)
		}
	}
}
