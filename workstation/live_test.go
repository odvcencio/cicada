package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestJSONLiveActionsKeepNotesPrivateAndCheckRevision(t *testing.T) {
	var revision atomic.Value
	revision.Store("initial")
	commands := make(chan map[string]any, 8)
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Source: "title \"Live\"", Revision: revision.Load().(string), Valid: true, Project: &project.Project{Title: "Live"}})
		case "/api/transport":
			if r.Method == http.MethodPost {
				var data map[string]any
				_ = json.NewDecoder(r.Body).Decode(&data)
				commands <- data
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"playing":true,"bar":1,"step":1}`))
		case "/api/params":
			_, _ = w.Write([]byte(`{"registry":[],"addresses":[]}`))
		case "/api/record", "/api/live":
			var data map[string]any
			_ = json.NewDecoder(r.Body).Decode(&data)
			commands <- data
			if r.URL.Path == "/api/record" && data["revision"] != revision.Load() {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"error":"score changed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(404)
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
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	jsonPost := func(path string, data map[string]string, token string) *http.Response {
		t.Helper()
		body, _ := json.Marshal(data)
		req, _ := http.NewRequest("POST", app.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", app.URL)
		req.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	input := map[string]string{"type": "note", "lease": "mounted-lease-123", "sequence": "1", "track": "bass", "note": "60", "velocity": "100", "on": "true"}
	if r := jsonPost("/__actions/launch", map[string]string{"revision": "initial", "quantize": "6", "command": `{"action":"slot","track":"bass","pattern":"pulse"}`}, csrf); r.StatusCode != 200 {
		t.Fatalf("launch: %d", r.StatusCode)
	}
	launch := <-commands
	if launch["action"] != "slot" || launch["track"] != "bass" || launch["pattern"] != "pulse" || launch["quantize"] != float64(6) {
		t.Fatalf("launch lost submitter or timing: %#v", launch)
	}
	if r := jsonPost("/__actions/live", input, ""); r.StatusCode != 403 {
		t.Fatalf("missing CSRF: %d", r.StatusCode)
	}
	if r := jsonPost("/__actions/live", input, csrf); r.StatusCode != 200 {
		t.Fatalf("live JSON: %d", r.StatusCode)
	}
	command := <-commands
	messages := command["messages"].([]any)
	note := messages[0].(map[string]any)
	if note["note"] != float64(60) || note["on"] != true || command["owner"] == input["lease"] {
		t.Fatalf("untyped or unscoped live request: %#v", command)
	}
	// New responses mask CSRF tokens independently. A held note and its
	// release must retain the same service owner across requests and renders.
	owner := command["owner"]
	freshCSRF := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	if freshCSRF == csrf {
		t.Fatal("CSRF token was not masked per response")
	}
	input["sequence"], input["on"] = "2", "false"
	if r := jsonPost("/__actions/live", input, freshCSRF); r.StatusCode != 200 {
		t.Fatalf("note off: %d", r.StatusCode)
	}
	if off := <-commands; off["owner"] != owner {
		t.Fatal("note off changed the live mount owner")
	}
	input["sequence"], input["type"] = "3", "release"
	if r := jsonPost("/__actions/live", input, freshCSRF); r.StatusCode != 200 {
		t.Fatalf("release: %d", r.StatusCode)
	}
	if release := <-commands; release["owner"] != owner || release["release"] != true {
		t.Fatal("release did not target the live mount owner")
	}
	if r := jsonPost("/__actions/note-preview", map[string]string{"revision": "initial", "recordings": `[{"track":"bass","pattern":"pulse","notes":[{"tick":0,"endTick":120,"note":60,"velocity":100}]}]`}, csrf); r.StatusCode != 200 {
		t.Fatalf("preview: %d", r.StatusCode)
	}
	if len(commands) != 0 {
		t.Fatal("preview wrote notes before commit")
	}
	otherJar, _ := cookiejar.New(nil)
	if strings.Contains(getPage(t, &http.Client{Jar: otherJar}, app.URL+"/?panel=live"), "Retained note take") {
		t.Fatal("take leaked to another session")
	}
	revision.Store("newer")
	form := url.Values{"csrf_token": {csrf}, "revision": {"newer"}, "intent": {"commit"}, "__gosx_return_to": {"/?panel=live"}}
	r := post(t, client, app.URL+"/__actions/note-commit", form, true, app.URL)
	r.Body.Close()
	if r.StatusCode != 409 {
		t.Fatalf("stale note commit: %d", r.StatusCode)
	}
	<-commands
	if !strings.Contains(getPage(t, client, app.URL+"/?panel=live"), "Apply notes to current score") {
		t.Fatal("conflicting note take was not retained for review")
	}
	form.Set("intent", "rebase")
	r = post(t, client, app.URL+"/__actions/note-commit", form, false, app.URL)
	r.Body.Close()
	if r.StatusCode != 303 {
		t.Fatalf("reviewed rebase: %d", r.StatusCode)
	}
	if command := <-commands; command["revision"] != "newer" {
		t.Fatal("rebase silently used a different disk revision")
	}
	if strings.Contains(getPage(t, client, app.URL+"/?panel=live"), "Retained note take") {
		t.Fatal("committed note take remained active")
	}
}
