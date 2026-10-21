package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx/action"
)

func TestEditorDraftRecoveryIsIndependentForEachFile(t *testing.T) {
	var mu sync.Mutex
	files := map[string]string{"main.cicada": "// saved main\n", "parts/lead.cicada": "// saved lead\n"}
	writes := 0
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{
				Revision: "project-current", Filename: "main.cicada", Source: files["main.cicada"], Valid: true,
				Files: []projectFile{
					{Name: "main.cicada", Source: files["main.cicada"], Revision: "main-current"},
					{Name: "parts/lead.cicada", Source: files["parts/lead.cicada"], Revision: "lead-current"},
				},
				Project: &project.Project{Title: "Draft recovery", TempoMilli: 110000, Key: project.Key{Scale: "minor"}},
			})
		case r.URL.Path == "/api/files" && r.Method == http.MethodPost:
			var edit struct{ File, Revision, Source, Action string }
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Error(err)
			}
			if edit.Action == "check" {
				_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
				return
			}
			if edit.Revision == "stale" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"file changed"}`))
				return
			}
			if strings.Contains(edit.Source, "invalid") {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"error":"invalid score"}`))
				return
			}
			if edit.File == "parts/lead.cicada" && edit.Revision != "lead-current" {
				t.Errorf("lead save used revision %q", edit.Revision)
			}
			files[edit.File] = edit.Source
			writes++
			_ = json.NewEncoder(w).Encode(map[string]string{"revision": edit.Revision})
		case r.Method == http.MethodPost:
			t.Errorf("file action reached unexpected route %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		default:
			_, _ = w.Write([]byte(`{}`))
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
	pageURL := func(file string) string { return app.URL + "/?panel=code&file=" + url.QueryEscape(file) }
	csrf := tokenFromPage(t, getPage(t, client, pageURL("main.cicada")))
	apply := func(file, revision, source, intent string) {
		t.Helper()
		form := url.Values{
			"csrf_token": {csrf}, "revision": {"project-current"}, "fileRevision": {revision},
			"file": {file}, "content": {source}, "intent": {intent},
			"__gosx_return_to": {"/?panel=code&file=" + url.QueryEscape(file)},
		}
		response := post(t, client, app.URL+"/__actions/source", form, false, app.URL)
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s %s status = %d", file, intent, response.StatusCode)
		}
	}
	assertDraft := func(file, marker string, want bool) {
		t.Helper()
		page := getPage(t, client, pageURL(file))
		if got := strings.Contains(page, marker); got != want {
			t.Fatalf("%s restored %q = %t, want %t", file, marker, got, want)
		}
		if got := strings.Contains(page, "Recovered draft. Not saved to disk."); got != want {
			t.Fatalf("%s recovered draft status = %t, want %t", file, got, want)
		}
	}

	apply("main.cicada", "stale", "// main conflict 🎵\n", "")
	// Consuming the rejected action while opening another file must not put
	// the main file's source into the lead editor.
	assertDraft("parts/lead.cicada", "main conflict", false)
	apply("parts/lead.cicada", "lead-current", "// invalid lead draft\n", "")
	assertDraft("main.cicada", "main conflict", true)
	assertDraft("parts/lead.cicada", "invalid lead draft", true)

	apply("parts/lead.cicada", "lead-current", "// checked lead draft é\n", "check")
	assertDraft("parts/lead.cicada", "checked lead draft", true)
	assertDraft("main.cicada", "main conflict", true)
	mu.Lock()
	if writes != 0 || files["parts/lead.cicada"] != "// saved lead\n" {
		t.Error("Check wrote the draft to disk")
	}
	mu.Unlock()

	// Separate GETs emulate reloads after the action flash was consumed.
	assertDraft("main.cicada", "main conflict", true)
	assertDraft("parts/lead.cicada", "checked lead draft", true)
	outsiderJar, _ := cookiejar.New(nil)
	outsider := &http.Client{Jar: outsiderJar}
	if strings.Contains(getPage(t, outsider, pageURL("main.cicada")), "main conflict") || strings.Contains(getPage(t, outsider, pageURL("parts/lead.cicada")), "checked lead draft") {
		t.Error("another workspace session could see the private drafts")
	}

	apply("parts/lead.cicada", "lead-current", "// new saved lead\n", "")
	assertDraft("parts/lead.cicada", "checked lead draft", false)
	assertDraft("main.cicada", "main conflict", true)
	apply("parts/lead.cicada", "lead-current", "// invalid second lead\n", "")
	apply("parts/lead.cicada", "lead-current", "", "discard")
	assertDraft("parts/lead.cicada", "invalid second lead", false)
	assertDraft("main.cicada", "main conflict", true)
	apply("main.cicada", "stale", "", "discard")
	assertDraft("main.cicada", "main conflict", false)

	// JSON actions without the optional reactive projection header must use
	// the same file, check/discard intent, and displayed merge revision.
	applyJSON := func(fields map[string]string) action.Result {
		t.Helper()
		fields["csrf_token"] = csrf
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, app.URL+"/__actions/source", bytes.NewReader(data))
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
		var result action.Result
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	checked := applyJSON(map[string]string{
		"file": "parts/lead.cicada", "fileRevision": "lead-current", "intent": "check", "content": "// JSON checked lead\n",
	})
	if !checked.OK || checked.Message != "File check passed. Draft is not saved." {
		t.Fatalf("JSON check: %+v", checked)
	}
	assertDraft("parts/lead.cicada", "JSON checked lead", true)
	merged := applyJSON(map[string]string{
		"file": "parts/lead.cicada", "fileRevision": "stale", "diskRevision": "lead-current", "intent": "merge", "content": "// JSON merged lead\n",
	})
	if !merged.OK {
		t.Fatalf("JSON merge: %+v", merged)
	}
	applyJSON(map[string]string{"file": "main.cicada", "fileRevision": "stale", "content": "// JSON conflict main\n"})
	assertDraft("main.cicada", "JSON conflict main", true)
	discarded := applyJSON(map[string]string{"file": "main.cicada", "intent": "discard"})
	if !discarded.OK {
		t.Fatalf("JSON discard: %+v", discarded)
	}
	assertDraft("main.cicada", "JSON conflict main", false)
	mu.Lock()
	defer mu.Unlock()
	if writes != 2 {
		t.Fatalf("save/discard wrote %d files, want only explicit save and merge", writes)
	}
}
