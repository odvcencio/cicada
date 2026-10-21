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

func TestEmptyPatternLibraryOffersCreationAndRetainsNativeErrors(t *testing.T) {
	// Audio-only arrangements can have no note patterns. A newly added
	// instrument still needs to be able to create its first grid here.
	p := &project.Project{Title: "First pattern", TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "tine_ep"}, {ID: "vox", Kind: "audio"}}, Scenes: []project.Scene{{ID: "main"}}}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", Valid: true, Project: p})
			return
		}
		_, _ = io.WriteString(w, `{}`)
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
	page := getPage(t, client, app.URL+"/?panel=patterns")
	for _, text := range []string{"Create your first pattern", `action="/__actions/pattern"`, `name="action" value="create"`, "keys · notes", "Library only", `name="length" value="16"`} {
		if !strings.Contains(page, text) {
			t.Fatalf("empty pattern library is missing %q", text)
		}
	}
	if strings.Contains(page, `<option value="vox"`) {
		t.Fatal("audio tracks were offered as note pattern destinations")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "action": {"create"}, "createFor": {""}, "newName": {"my-melody"}, "track": {"keys"}, "scene": {"main"}, "length": {"66"}, "__gosx_return_to": {"/?panel=patterns"}}
	response := post(t, client, app.URL+"/__actions/pattern", form, false, app.URL)
	if response.StatusCode != 303 {
		t.Fatalf("native validation did not redirect: %d", response.StatusCode)
	}
	page = getPage(t, client, app.URL+response.Header.Get("Location"))
	for _, text := range []string{`name="newName" value="my-melody"`, `name="length" value="66"`, "pattern length must be 1–64 steps"} {
		if !strings.Contains(page, text) {
			t.Fatalf("native validation lost %q", text)
		}
	}
}
