package main

import (
	"bytes"
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

func noteRecoveryApp(t *testing.T, dir string) *httptest.Server {
	return configuredNoteRecoveryApp(t, dir, false)
}

func configuredNoteRecoveryApp(t *testing.T, dir string, local bool) *httptest.Server {
	t.Helper()
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "recorded", Valid: true, Project: &project.Project{Title: "Private recovery", TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "acid"}}, Patterns: []project.Pattern{{ID: "phrase", Kind: "acid", Steps: 16}}}})
		case "/api/params":
			io.WriteString(w, `{"registry":[],"addresses":[]}`)
		case "/api/transport":
			io.WriteString(w, `{"playing":false,"bar":1,"step":1}`)
		case "/api/intents":
			io.WriteString(w, `{"valid":true,"dryRun":true}`)
		case "/api/record":
			io.WriteString(w, `{"valid":true,"revision":"applied"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(audio.Close)
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newConfiguredApp(b, workspaceOptions{StateDir: dir, LocalBootstrap: local})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	return app
}

func noteJSONPost(t *testing.T, client *http.Client, origin, path, csrf string, data map[string]string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest(http.MethodPost, origin+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestPrivateNoteReviewSurvivesServiceRestartAndPortChange(t *testing.T) {
	dir := t.TempDir()
	app := noteRecoveryApp(t, dir)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	id := strings.Repeat("a", 32)
	values := map[string]string{"takeId": id, "sequence": "1", "revision": "recorded", "recordings": `[{"track":"keys","pattern":"phrase","notes":[{"tick":3857,"endTick":4110,"note":64,"velocity":105,"input":{"hostNanos":1000000,"endHostNanos":2000000,"clockEpoch":7,"source":"source","endSource":"source"}}]}]`}
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-checkpoint", "", values); r.StatusCode != 403 {
		t.Fatal("checkpoint accepted without CSRF", r.StatusCode)
	}
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-checkpoint", csrf, values); r.StatusCode != 200 {
		data, _ := io.ReadAll(r.Body)
		t.Fatalf("checkpoint: %d %s", r.StatusCode, data)
	}
	values["sequence"] = "2"
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-preview", csrf, values); r.StatusCode != 200 {
		t.Fatal("finish failed", r.StatusCode)
	}
	oldURL := app.URL
	app.Close()
	app = noteRecoveryApp(t, dir)
	if app.URL == oldURL {
		t.Fatal("restart test did not change origin port")
	}
	page := getPage(t, client, app.URL+"/?panel=live")
	for _, want := range []string{"Retained note take", "E4", "2.1.1 +17 ticks"} {
		if !strings.Contains(page, want) {
			t.Fatalf("restart lost %q", want)
		}
	}
	r, err := client.Get(app.URL + "/api/note-recovery/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var saved savedNotes
	if err = json.NewDecoder(r.Body).Decode(&saved); err != nil || saved.Sequence != 2 || saved.Takes[0].Notes[0].Input.HostNanos != 1000000 {
		t.Fatalf("recovery lost original source time: %+v %v", saved, err)
	}
	freshJar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: freshJar}
	otherPage := getPage(t, other, app.URL+"/?panel=live")
	if strings.Contains(otherPage, "Retained note take") || strings.Contains(otherPage, "Saved note performances") {
		t.Fatal("saved notes leaked to another session")
	}
	r, err = other.Get(app.URL + "/api/note-recovery/" + id)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("another session read a known note receipt")
	}
	csrf = tokenFromPage(t, page)
	checked := url.Values{"csrf_token": {csrf}, "revision": {"recorded"}, "intent": {"check"}, "__gosx_return_to": {"/?panel=live"}}
	r = post(t, client, app.URL+"/__actions/note-commit", checked, false, app.URL)
	r.Body.Close()
	if r.StatusCode != 303 || !strings.Contains(getPage(t, client, app.URL+"/?panel=live"), "Pattern check passed") {
		t.Fatal("restarted review lost its validation result")
	}
	values["sequence"] = "999"
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-checkpoint", csrf, values); r.StatusCode != 409 {
		t.Fatal("late checkpoint replaced ready performance", r.StatusCode)
	}
	form := url.Values{"csrf_token": {csrf}, "revision": {"recorded"}, "intent": {"discard"}, "__gosx_return_to": {"/?panel=live"}}
	r = post(t, client, app.URL+"/__actions/note-commit", form, false, app.URL)
	r.Body.Close()
	if r.StatusCode != 303 {
		t.Fatal("discard failed", r.StatusCode)
	}
	app.Close()
	app = noteRecoveryApp(t, dir)
	if page = getPage(t, client, app.URL+"/?panel=live"); strings.Contains(page, "Retained note take") || strings.Contains(page, "Saved note performances") {
		t.Fatal("discarded performance revived after restart")
	}
	r, err = client.Get(app.URL + "/api/note-recovery/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if err = json.NewDecoder(r.Body).Decode(&saved); err != nil || saved.State != "discarded" || len(saved.Takes) != 1 {
		t.Fatal("discard lost original instead of archiving it")
	}
	viewerPage := getPage(t, other, app.URL+"/?panel=live")
	if r := noteJSONPost(t, other, app.URL, "/__actions/note-checkpoint", tokenFromPage(t, viewerPage), values); r.StatusCode != 403 {
		t.Fatal("restart granted a viewer write access", r.StatusCode)
	}
	separate := noteRecoveryApp(t, t.TempDir())
	if page := getPage(t, client, separate.URL+"/?panel=live"); strings.Contains(page, "Saved note performances") || strings.Contains(page, "Retained note take") {
		t.Fatal("different workspace inherited private recovery")
	}
	history := getPage(t, client, app.URL+"/?panel=live&noteHistory=on")
	if !strings.Contains(history, "Dismissed checkpoint") {
		t.Fatal("handled performance is not recoverable through the UI")
	}
	copyForm := url.Values{"csrf_token": {tokenFromPage(t, history)}, "revision": {"recorded"}, "takeId": {id}, "intent": {"review"}, "__gosx_return_to": {"/?panel=live"}}
	r = post(t, client, app.URL+"/__actions/note-restore", copyForm, false, app.URL)
	r.Body.Close()
	if r.StatusCode != 303 || !strings.Contains(getPage(t, client, app.URL+"/?panel=live"), "Retained note take") {
		t.Fatal("dismissed performance could not be reviewed as a new copy")
	}
}

func TestInterruptedNoteCheckpointCanBeReviewedWithoutStealingWriter(t *testing.T) {
	dir := t.TempDir()
	app := noteRecoveryApp(t, dir)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	id := strings.Repeat("b", 32)
	values := map[string]string{"takeId": id, "sequence": "1", "revision": "recorded", "recordings": `[{"track":"keys","pattern":"phrase","notes":[{"tick":3857,"endTick":4110,"note":64,"velocity":105,"endEstimated":true}]}]`}
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-checkpoint", csrf, values); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	app.Close()
	app = noteRecoveryApp(t, dir)
	page := getPage(t, client, app.URL+"/?panel=live")
	if !strings.Contains(page, "Last recording checkpoint") {
		t.Fatal("interrupted recording was not recoverable")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"recorded"}, "takeId": {id}, "__gosx_return_to": {"/?panel=live"}}
	r := post(t, client, app.URL+"/__actions/note-restore", form, false, app.URL)
	r.Body.Close()
	if r.StatusCode != 303 {
		t.Fatal("recovery failed", r.StatusCode)
	}
	page = getPage(t, client, app.URL+"/?panel=live")
	if !strings.Contains(page, "Retained note take") || !strings.Contains(page, "· estimated") {
		t.Fatal("checkpoint did not preserve note or estimated release")
	}
	values["sequence"] = "2"
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-checkpoint", tokenFromPage(t, page), values); r.StatusCode != 200 {
		t.Fatal("review stole original recording writer", r.StatusCode)
	}
}
