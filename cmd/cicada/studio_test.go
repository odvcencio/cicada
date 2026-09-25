package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const studioScore = "title \"Studio\"\ntrack bass acid {}\ntrack drums drums {}\npattern pulse acid steps=4 { 1 . 5 . }\npattern beat drums steps=4 { bd: x... sd: .... }\nscene main { bass=pulse drums=beat }\nsong { main }\n"

func studioTestHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	return handler, path
}

func studioCall(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var input bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&input).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	request := httptest.NewRequest(method, path, &input)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestStudioProjectsAndTogglesSource(t *testing.T) {
	handler, path := studioTestHandler(t)
	page := studioCall(t, handler, "/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="source-editor"`) || !strings.Contains(page.Body.String(), `data-pattern="pulse"`) || !strings.Contains(page.Body.String(), `data-lane="bd"`) {
		t.Fatalf("studio page: %d %s", page.Code, page.Body.String())
	}
	revision := studioRevision([]byte(studioScore))
	updated := studioCall(t, handler, "/api/toggle", studioEdit{Revision: revision, Pattern: "pulse", Step: 0})
	if updated.Code != 200 {
		t.Fatalf("note toggle: %d %s", updated.Code, updated.Body.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("{ . . 5 . }")) {
		t.Fatalf("note source unchanged: %s", content)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("score mode: %v, %v", info, err)
	}
	stale := studioCall(t, handler, "/api/toggle", studioEdit{Revision: revision, Pattern: "beat", Lane: "bd", Step: 0})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale edit accepted: %d", stale.Code)
	}
	revision = studioRevision(content)
	updated = studioCall(t, handler, "/api/toggle", studioEdit{Revision: revision, Pattern: "beat", Lane: "bd", Step: 0})
	if updated.Code != 200 {
		t.Fatalf("drum toggle: %d %s", updated.Code, updated.Body.String())
	}
	content, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(content, []byte("bd: ....")) {
		t.Fatalf("drum source unchanged: %s, %v", content, err)
	}
}

func TestStudioValidatesSourceAndKeepsLastGoodProjection(t *testing.T) {
	handler, path := studioTestHandler(t)
	revision := studioRevision([]byte(studioScore))
	invalid := strings.Replace(studioScore, "1 . 5 .", "8 . 5 .", 1)
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: revision, Source: invalid})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid source accepted: %d %s", response.Code, response.Body.String())
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != studioScore {
		t.Fatalf("invalid source was written: %v", err)
	}
	valid := strings.Replace(studioScore, "Studio", "Night Studio", 1)
	response = studioCall(t, handler, "/api/source", studioEdit{Revision: revision, Source: valid})
	if response.Code != 200 {
		t.Fatalf("valid source rejected: %d %s", response.Code, response.Body.String())
	}
	if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	state := studioCall(t, handler, "/api/state", nil)
	if state.Code != 200 || !strings.Contains(state.Body.String(), `"valid":false`) || !strings.Contains(state.Body.String(), `"source":`) {
		t.Fatalf("invalid disk state: %d %s", state.Code, state.Body.String())
	}
	page := studioCall(t, handler, "/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Night Studio") {
		t.Fatalf("last good projection lost: %d %s", page.Code, page.Body.String())
	}
}

func TestStudioRejectsCrossOriginEdits(t *testing.T) {
	handler, path := studioTestHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/api/source", strings.NewReader(`{"revision":"bad","source":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://outside.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin edit accepted: %d", recorder.Code)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != studioScore {
		t.Fatalf("score changed: %v", err)
	}
}

func TestStudioGridEditFollowsSharedPhraseSource(t *testing.T) {
	source := "title \"Phrase\"\ntrack bass acid {}\nphrase hook { 1 . }\npattern pulse { use hook*2 }\nscene main { bass=pulse }\nsong { main }\n"
	updated, err := toggledSource([]byte(source), "pulse", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "phrase hook { . . }") || !strings.Contains(string(updated), "use hook*2") {
		t.Fatalf("phrase source was not edited in place: %s", updated)
	}
}
