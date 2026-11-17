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
	"m31labs.dev/gosx"
)

func TestStructureActions(t *testing.T) {
	for _, tc := range []struct{ name, kind, field, value, message string }{
		{"scene-rename", "rename", "entity", "scene:main", "Scene renamed."},
		{"scene-duplicate", "duplicatescene", "scene", "main", "Scene duplicated."},
		{"scene-add", "addscene", "name", "new", "Scene created."},
		{"scene-delete", "deletescene", "scene", "main", "Scene deleted."},
		{"track-delete", "deletetrack", "track", "bass", "Track deleted."},
		{"song-convert", "convertsongtoarrange", "kind", "convertsongtoarrange", "Song converted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			calls := 0
			audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/workspace" {
					_ = json.NewEncoder(w).Encode(workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: &project.Project{Edition: 1, Tracks: []project.Track{{ID: "bass", Kind: "acid"}}, Scenes: []project.Scene{{ID: "main"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}}}})
					return
				}
				if r.URL.Path == "/api/intents" {
					calls++
					if r.Header.Get("X-Cicada-Source-Revision") != "sources" {
						t.Error("missing source receipt")
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if calls == 2 {
						w.WriteHeader(422)
						_, _ = io.WriteString(w, `{"error":"refused"}`)
						return
					}
				}
				_, _ = io.WriteString(w, `{}`)
			}))
			defer audio.Close()
			b, err := newBackend(audio.URL)
			if err != nil {
				t.Fatal(err)
			}
			h, err := newApp(b)
			if err != nil {
				t.Fatal(err)
			}
			app := httptest.NewServer(h)
			defer app.Close()
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			page := getPage(t, client, app.URL+"/?panel=session")
			f := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"sources"}, "scene": {"main"}, "track": {"bass"}, "name": {"new"}}
			resp := post(t, client, app.URL+"/__actions/"+tc.name, f, true, app.URL)
			if resp.StatusCode >= 400 {
				t.Fatalf("success: %d", resp.StatusCode)
			}
			var success struct {
				Redirect string `json:"redirect"`
				Message  string `json:"message"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&success); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if success.Redirect == "" || !strings.Contains(success.Message, tc.message) {
				t.Fatalf("redirect: %+v", success)
			}
			intents := body["intents"].([]any)
			intent := intents[0].(map[string]any)
			if (tc.name == "scene-rename" || tc.name == "scene-duplicate") && intent["name"] != "new" {
				t.Fatalf("name: %v", intent)
			}
			if len(intents) != 1 || body["revision"] != "current" || body["version"] != float64(1) || intent["kind"] != tc.kind || intent[tc.field] != tc.value {
				t.Fatalf("envelope: %v", body)
			}
			resp = post(t, client, app.URL+"/__actions/"+tc.name, f, true, app.URL)
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 422 || !strings.Contains(string(data), "refused") || !strings.Contains(string(data), "new") {
				t.Fatalf("refusal: %d %s", resp.StatusCode, data)
			}
			f.Del("source_revision")
			resp = post(t, client, app.URL+"/__actions/"+tc.name, f, true, app.URL)
			resp.Body.Close()
			if resp.StatusCode != 409 || calls != 2 {
				t.Fatalf("conflict: %d, calls %d", resp.StatusCode, calls)
			}
			f.Set("source_revision", "sources")
			f.Del("revision")
			resp = post(t, client, app.URL+"/__actions/"+tc.name, f, true, app.URL)
			resp.Body.Close()
			if resp.StatusCode != 409 || calls != 2 {
				t.Fatalf("missing revision: %d, calls %d", resp.StatusCode, calls)
			}
		})
	}
}

func TestStructureMarkup(t *testing.T) {
	p := &project.Project{Edition: 1, Tracks: []project.Track{{ID: "bass", Kind: "acid"}}, Scenes: []project.Scene{{ID: "main"}, {ID: "main-2"}, {ID: "unused"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "main", Bars: 2}}}
	html := gosx.RenderHTML((&studioApp{}).arrangementView(nil, workspace{Project: p, Revision: "r", SourceRevision: "s"}, "csrf", "", true))
	for _, want := range []string{`aria-label="Scene"`, `Scene main · used by 2 sections`, `value="main-3"`, `disabled`, `This scene is used by the song.`, `Delete scene unused`, `Delete track bass`, `Convert to placements`, `>Convert</button>`, `name="source_revision" value="s"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
}
