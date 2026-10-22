package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartupScriptRevalidatesItsExactContent(t *testing.T) {
	first := httptest.NewRecorder()
	serveStartupScript(first, httptest.NewRequest(http.MethodGet, "/studio-startup.js", nil))
	if first.Code != http.StatusOK || first.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || first.Body.Len() == 0 {
		t.Fatal("startup helper unavailable")
	}
	request := httptest.NewRequest(http.MethodGet, "/studio-startup.js", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	serveStartupScript(second, request)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatal("unchanged startup helper was not revalidated")
	}
}
