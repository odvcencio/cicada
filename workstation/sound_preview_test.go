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

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx/action"
)

func TestSoundBrowserUsesPrivateActionsWithoutEditingOrNavigating(t *testing.T) {
	var calls []map[string]any
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			json.NewEncoder(w).Encode(workspace{Revision: "current", Valid: true, Project: &project.Project{Title: "Choose sounds", TempoMilli: 130000, Key: project.Key{Root: 9, Scale: "minor"}}})
		case "/api/sound-preview":
			var call map[string]any
			if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
				t.Error(err)
			}
			calls = append(calls, call)
			io.WriteString(w, `{"lease":"private-preview","expiresMs":600000,"active":true,"durationMs":4846}`)
		case "/api/instrument", "/api/source", "/api/intents":
			t.Errorf("audition attempted a score mutation: %s", r.URL.Path)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer service.Close()
	b, _ := newBackend(service.URL)
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	page := getPage(t, client, app.URL+"/?panel=instruments")
	if strings.Index(page, `id="instrument-library"`) > strings.Index(page, `id="recorded-instruments"`) {
		t.Fatal("hardware setup precedes the built-in sounds")
	}
	for _, patch := range instrument.Patches() {
		if !strings.Contains(page, `aria-label="Listen to `+patch.Name+`"`) {
			t.Fatalf("sound %s cannot be auditioned", patch.Name)
		}
	}
	csrf := tokenFromPage(t, page)
	for _, command := range []string{"open", "play", "stop", "close"} {
		fields := url.Values{"csrf_token": {csrf}, "action": {command}, "lease": {"private-preview"}, "sequence": {"3"}, "patch": {"warm-pad"}, "tempo": {"130000"}, "root": {"9"}, "scale": {"minor"}}
		r := post(t, client, app.URL+"/__actions/sound-preview", fields, true, app.URL)
		var result action.Result
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != 200 || !result.OK || result.Redirect != "" {
			t.Fatalf("%s navigated or failed: %d %+v", command, r.StatusCode, result)
		}
	}
	if len(calls) != 4 || calls[1]["tempo"] != float64(130000) || calls[1]["root"] != float64(9) || calls[1]["patch"] != "warm-pad" {
		t.Fatalf("lost audition context: %+v", calls)
	}
	invalid := post(t, client, app.URL+"/__actions/sound-preview", url.Values{"csrf_token": {csrf}, "action": {"play"}, "sequence": {"-1"}}, true, app.URL)
	if invalid.StatusCode != 422 || len(calls) != 4 {
		t.Fatal("invalid sequence reached audio")
	}
	forbidden := post(t, client, app.URL+"/__actions/sound-preview", url.Values{"action": {"open"}}, true, app.URL)
	if forbidden.StatusCode != 403 || len(calls) != 4 {
		t.Fatal("sound preview bypassed CSRF")
	}
}
