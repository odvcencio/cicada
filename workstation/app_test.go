package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func testApp(t *testing.T) (string, *http.Client, *[]map[string]any) {
	t.Helper()
	var edits []map[string]any
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", Filename: "test.cicada", Source: "title \"Test\"\n", Valid: true, Project: &project.Project{Title: "Test", TempoMilli: 130000, Key: project.Key{Scale: "minor"}}})
		case "/api/transport":
			_ = json.NewEncoder(w).Encode(transport{Bar: 1, Step: 1})
		case "/api/source", "/api/toggle", "/api/pattern", "/api/song", "/api/project", "/api/export":
			var edit map[string]any
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Error(err)
			}
			edits = append(edits, edit)
			if edit["revision"] != "current" {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"error":"score changed"}`)
			} else if source, _ := edit["source"].(string); strings.Contains(source, "invalid") {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"error":"invalid score"}`)
			} else {
				_, _ = io.WriteString(w, `{"revision":"new","valid":true}`)
			}
		default:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":"unavailable"}`)
		}
	}))
	t.Cleanup(audio.Close)
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return app.URL, client, &edits
}

func TestBuildWorkspaceRemainsPrivateAndHasNoDomainRoutes(t *testing.T) {
	handler, err := newApp(nil)
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Cache-Control"), "no-store") || len(page.Result().Cookies()) != 0 {
		t.Fatalf("build workspace status=%d cache=%q cookies=%d", page.Code, page.Header().Get("Cache-Control"), len(page.Result().Cookies()))
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace", nil))
	if api.Code != http.StatusNotFound {
		t.Fatalf("build API status=%d", api.Code)
	}
}

func TestBackendCredentialStaysOnServer(t *testing.T) {
	t.Setenv("CICADA_BACKEND_TOKEN", "private-test-credential")
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-credential" {
			t.Error("missing private credential")
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer audio.Close()
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.call(t.Context(), http.MethodGet, "/api/state", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func getPage(t *testing.T, client *http.Client, address string) string {
	t.Helper()
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("page: %d %s", response.StatusCode, data)
	}
	return string(data)
}
func tokenFromPage(t *testing.T, page string) string {
	t.Helper()
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("page lacks a CSRF token")
	}
	return match[1]
}
func post(t *testing.T, client *http.Client, address string, form url.Values, managed bool, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", address, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if managed {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-CSRF-Token", form.Get("csrf_token"))
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestActionsRequireCSRFAndRejectForeignOrigin(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"revision": {"current"}, "content": {"title \"Saved\"\n"}}
	if response := post(t, client, address+"/__actions/source", form, true, address); response.StatusCode != 403 {
		t.Fatalf("missing token status: %d", response.StatusCode)
	}
	form.Set("csrf_token", csrf)
	if response := post(t, client, address+"/__actions/source", form, true, "http://evil.example"); response.StatusCode != 403 {
		t.Fatalf("foreign origin status: %d", response.StatusCode)
	}
	if len(*edits) != 0 {
		t.Fatal("rejected requests reached the audio service")
	}
	form.Set("__gosx_return_to", "/?panel=code#score")
	response := post(t, client, address+"/__actions/source", form, true, address)
	var result struct {
		OK       bool   `json:"ok"`
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Redirect != "/?panel=code#score" {
		t.Fatalf("managed result: %+v", result)
	}
	if len(*edits) != 1 || (*edits)[0]["source"] != form.Get("content") {
		t.Fatalf("unexpected audio edits: %+v", *edits)
	}
}

func TestNativeInvalidLargeDraftSurvivesRedirect(t *testing.T) {
	address, client, _ := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	draft := strings.Repeat("// preserved draft\n", 3000) + "invalid score\n"
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "content": {draft}, "__gosx_return_to": {"/?panel=code"}}
	response := post(t, client, address+"/__actions/source", form, false, address)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/?panel=code" {
		t.Fatalf("native validation: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	for _, cookie := range response.Cookies() {
		if len(cookie.Value) > 4096 {
			t.Fatal("draft was put in a cookie")
		}
	}
	page := getPage(t, client, address+response.Header.Get("Location"))
	if !strings.Contains(page, "invalid score") || !strings.Contains(page, "// preserved draft") {
		t.Fatal("native redirect lost the draft")
	}
	if !strings.Contains(getPage(t, client, address+"/?panel=code"), "// preserved draft") {
		t.Fatal("reload lost the rejected draft")
	}
	// Another browser cannot select or see the rejected source via its receipt.
	jar, _ := cookiejar.New(nil)
	outsider := &http.Client{Jar: jar}
	if page := getPage(t, outsider, address+"/?panel=code"); strings.Contains(page, "// preserved draft") {
		t.Fatal("private draft leaked to another browser")
	}
}

func TestConflictingDraftCanMergeOrDiscardWithoutOverwritingNewerRevision(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"older"}, "content": {"title \"My draft\"\n"}, "__gosx_return_to": {"/?panel=code"}}
	response := post(t, client, address+"/__actions/source", form, false, address)
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("conflict status=%d", response.StatusCode)
	}
	page := getPage(t, client, address+"/?panel=code")
	if !strings.Contains(page, "Save combined draft") || !strings.Contains(page, `name="diskRevision" value="current"`) || !strings.Contains(page, "My draft") || !strings.Contains(page, `<details open><summary>Current file:`) {
		t.Fatal("conflict has no reviewable merge path")
	}
	form.Set("intent", "merge")
	form.Set("diskRevision", "current")
	form.Set("content", "title \"Combined\"\n")
	response = post(t, client, address+"/__actions/source", form, false, address)
	response.Body.Close()
	if response.StatusCode != 303 || (*edits)[len(*edits)-1]["revision"] != "current" {
		t.Fatal("merge did not use the displayed revision")
	}
	if strings.Contains(getPage(t, client, address+"/?panel=code"), "Discard draft") {
		t.Fatal("successful merge retained rejected draft")
	}
	form.Set("intent", "")
	form.Set("revision", "current")
	form.Set("content", "invalid draft")
	response = post(t, client, address+"/__actions/source", form, false, address)
	response.Body.Close()
	count := len(*edits)
	form.Set("intent", "discard")
	response = post(t, client, address+"/__actions/source", form, false, address)
	response.Body.Close()
	if len(*edits) != count || strings.Contains(getPage(t, client, address+"/?panel=code"), "invalid draft") {
		t.Fatal("discard mutated the file or retained the draft")
	}
}

func TestSimultaneousWorkspacesKeepIndependentSessions(t *testing.T) {
	first, client, _ := testApp(t)
	second, _, _ := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, first+"/?panel=code"))
	getPage(t, client, second+"/?panel=code")
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "content": {"title \"Still owned\"\n"}, "__gosx_return_to": {"/?panel=code"}}
	response := post(t, client, first+"/__actions/source", form, true, first)
	defer response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("second workspace replaced the first session: %d", response.StatusCode)
	}
}

func TestBackendOriginAndPrivateRoutes(t *testing.T) {
	for _, value := range []string{"https://127.0.0.1:80", "http://example.com", "http://user@localhost:80", "http://localhost:80/api", "http://localhost:80?x=1"} {
		if _, err := newBackend(value); err == nil {
			t.Fatalf("accepted backend %s", value)
		}
	}
	address, client, _ := testApp(t)
	getPage(t, client, address+"/")
	response, err := client.Get(address + "/studio-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("legacy script route remains active: %d", response.StatusCode)
	}
	req, _ := http.NewRequest("GET", address+"/", nil)
	req.Host = "evil.example"
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("foreign host: %d", response.StatusCode)
	}
}

func TestHighlightUTF16Offsets(t *testing.T) {
	text := "Aé🎵Z"
	got := utf16Offsets(text)
	for index, want := range map[int]int{0: 0, 1: 1, 3: 2, 7: 4, 8: 5} {
		if got[index] != want {
			t.Fatalf("byte %d: %d != %d", index, got[index], want)
		}
	}
}
