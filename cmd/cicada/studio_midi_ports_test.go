package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStudioMIDIPortsAreLocalAndFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte("cicada 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &studio{path: path}
	get := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost/api/midi/ports", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.midiPorts(w, r)
		return w
	}
	if got := get("https://outside.example"); got.Code != 403 {
		t.Fatalf("foreign origin: %d", got.Code)
	}
	if got := get(""); got.Code != 200 || got.Body.String() != "{\"ports\":{}}\n" {
		t.Fatalf("absent config: %d %s", got.Code, got.Body.String())
	}
	local := filepath.Join(dir, "cicada.local")
	if err := os.WriteFile(local, []byte("port keys = \"Synthetic Keys\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got := get("")
	var body struct {
		Ports map[string]string `json:"ports"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &body) != nil || body.Ports["keys"] != "Synthetic Keys" {
		t.Fatalf("valid config: %d %s", got.Code, got.Body.String())
	}
	if err := os.WriteFile(local, []byte("port keys = \"one\"\nport keys = \"two\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := get(""); got.Code != 422 {
		t.Fatalf("invalid config: %d %s", got.Code, got.Body.String())
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), local); err != nil {
		t.Fatal(err)
	}
	if got := get(""); got.Code == 200 {
		t.Fatal("followed external config symlink")
	}
}
