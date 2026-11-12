package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Code recovery reads a file's revision on every checkpoint. Musical edits and
// transport commands hold s.mu, so the read must not wait for it, and it
// reads only the one file it names.
func TestFileRevisionTakesNoStudioLock(t *testing.T) {
	path := copyStudioProject(t)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	h := s.routes()
	entry, _ := os.ReadFile(path)
	part, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "parts/patterns.cicada"))
	read := func(file string) *httptest.ResponseRecorder {
		t.Helper()
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			request := httptest.NewRequest("GET", "/api/files/revision?file="+url.QueryEscape(file), nil)
			request.Host = "127.0.0.1:1234"
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			done <- recorder
		}()
		select {
		case recorder := <-done:
			return recorder
		case <-time.After(10 * time.Second):
			t.Fatalf("the revision read of %q waited for the Studio lock", file)
			return nil
		}
	}
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	for _, c := range []struct {
		file string
		text []byte
	}{{"", entry}, {"main.cicada", entry}, {"parts/patterns.cicada", part}, {"parts/patterns.cicada", part}} {
		start := time.Now()
		response := read(c.file)
		var body struct{ Revision string }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Revision != studioRevision(c.text) {
			t.Fatalf("%q: %d %s", c.file, response.Code, response.Body)
		}
		t.Logf("%q read in %v", c.file, time.Since(start))
	}
	for _, file := range []string{"../escape.cicada", "parts/missing.cicada", "cicada.mod"} {
		if response := read(file); response.Code != 404 {
			t.Fatalf("%q outside the project's sources answered %d %s", file, response.Code, response.Body)
		}
	}
	// The answer follows the file on disk.
	edited := append(append([]byte{}, part...), "\n// edited\n"...)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "parts/patterns.cicada"), edited, 0644); err != nil {
		t.Fatal(err)
	}
	var body struct{ Revision string }
	if response := read("parts/patterns.cicada"); json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Revision != studioRevision(edited) {
		t.Fatalf("the revision did not follow the file: %s", response.Body)
	}
	s.mu.Unlock()
	locked = false
}
