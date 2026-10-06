package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStudioCollaborationStoreIsPrivateAndDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	s := &studio{path: path}
	if err := os.WriteFile(path, []byte("original score"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := s.domainRoutes()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/collaboration/store", strings.NewReader(`{"id":"draft","document":{"operations":[]}}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("store: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/collaboration/store", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"draft"`) {
		t.Fatal("stored draft was not restored")
	}
	info, err := os.Stat(path + ".collaboration")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("draft is not private: info=%v err=%v", info, err)
	}
	source, _ := os.ReadFile(path)
	if string(source) != "original score" {
		t.Fatal("draft store changed the score")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/collaboration/store", strings.NewReader("invalid")))
	if response.Code != 400 {
		t.Fatal("invalid draft accepted")
	}
}
