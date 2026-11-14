package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestTrackRenameCarriesVersionAndProjectReceipt(t *testing.T) {
	var requests []struct {
		body    map[string]any
		receipt string
	}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: &project.Project{Edition: 1, Tracks: []project.Track{{ID: "bass", Kind: "acid"}}, Scenes: []project.Scene{{ID: "main"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}}}})
		case "/api/intents":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			requests = append(requests, struct {
				body    map[string]any
				receipt string
			}{body, r.Header.Get("X-Cicada-Source-Revision")})
			if len(requests) == 2 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"error":"name already exists"}`)
				return
			}
			_, _ = io.WriteString(w, `{"revision":"next"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
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
	app := httptest.NewServer(handler)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, app.URL+"/?panel=session")
	for _, text := range []string{`aria-label="Rename bass"`, `aria-label="New name for bass"`, `name="source_revision" value="all-sources"`} {
		if !strings.Contains(page, text) {
			t.Fatalf("rename control missing %s", text)
		}
	}
	fields := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"all-sources"}, "track": {"bass"}, "name": {"low"}}
	if response := post(t, client, app.URL+"/__actions/track-rename", fields, true, app.URL); response.StatusCode >= 400 {
		t.Fatalf("rename: %d", response.StatusCode)
	}
	if len(requests) != 1 || requests[0].receipt != "all-sources" {
		t.Fatalf("source receipt: %+v", requests)
	}
	body := requests[0].body
	intents, ok := body["intents"].([]any)
	if !ok || len(intents) != 1 {
		t.Fatalf("intents: %+v", body)
	}
	intent := intents[0].(map[string]any)
	if body["version"] != float64(1) || body["revision"] != "current" || intent["kind"] != "rename" || intent["entity"] != "track:bass" || intent["name"] != "low" {
		t.Fatalf("intent: %+v", body)
	}
	fields.Set("name", "other")
	response := post(t, client, app.URL+"/__actions/track-rename", fields, true, app.URL)
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(data), "name already exists") {
		t.Fatalf("refusal: %d %s", response.StatusCode, data)
	}
	fields.Del("source_revision")
	response = post(t, client, app.URL+"/__actions/track-rename", fields, true, app.URL)
	if response.StatusCode != http.StatusConflict || len(requests) != 2 {
		t.Fatalf("missing receipt: %d requests=%d", response.StatusCode, len(requests))
	}
}
