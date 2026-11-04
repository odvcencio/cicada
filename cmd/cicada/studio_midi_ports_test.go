package main

import (
	"bytes"
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
	if got := get(""); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte(`"ports":{}`)) {
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

func TestStudioMIDIPortBindingPreservesAndProtectsLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	score := "cicada 2\ntempo 120\ntrack bass acid {}\nmidi { port keys }\npattern p { 1 . }\nscene a { bass = p }\nsong { a }\n"
	if err := os.WriteFile(path, []byte(score), 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, "cicada.local")
	original := []byte("# My device\nport keys = \"Old\" # keep\nport pads = \"Pads\"\n")
	if err := os.WriteFile(local, original, 0600); err != nil {
		t.Fatal(err)
	}
	s := &studio{path: path}
	post := func(port, device, revision, origin string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]string{"port": port, "device": device, "revision": revision})
		r := httptest.NewRequest("POST", "http://localhost/api/midi/ports", bytes.NewReader(data))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.setMIDIPort(w, r)
		return w
	}
	rev := midiPortsRevision(original)
	if got := post("keys", "New", rev, "https://outside.example"); got.Code != 403 {
		t.Fatalf("cross-origin: %d", got.Code)
	}
	if got := post("other", "New", rev, ""); got.Code != 422 {
		t.Fatalf("undeclared: %d %s", got.Code, got.Body.String())
	}
	if got := post("keys", "New", "stale", ""); got.Code != 409 {
		t.Fatalf("stale: %d %s", got.Code, got.Body.String())
	}
	if got := post("keys", "New", rev, ""); got.Code != 200 {
		t.Fatalf("save: %d %s", got.Code, got.Body.String())
	}
	updated, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "# My device\nport keys = \"New\" # keep\nport pads = \"Pads\"\n" {
		t.Fatalf("changed unrelated config: %q", updated)
	}
	if got := post("keys", "Again", rev, ""); got.Code != 409 {
		t.Fatalf("old revision: %d", got.Code)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, local); err != nil {
		t.Fatal(err)
	}
	if got := post("keys", "No", midiPortsRevision(nil), ""); got.Code == 200 {
		t.Fatal("allowed a symlink")
	}
	if data, _ := os.ReadFile(outside); string(data) != "safe" {
		t.Fatal("modified outside file")
	}
}
