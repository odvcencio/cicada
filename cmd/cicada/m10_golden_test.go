package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// m10Result is the outcome of one edit: the new source, or the error text.
type m10Result struct {
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

func m10Outcome(source []byte, err error) m10Result {
	if err != nil {
		return m10Result{Error: err.Error()}
	}
	return m10Result{Source: string(source)}
}

func m10GoldenPath(group string) string { return filepath.Join("testdata", "m10", group+".json") }

// m10Golden loads the recorded outcomes of the retired writers. They pin exact
// bytes and error text now that the writers are gone.
func m10Golden(t *testing.T, group string) map[string]m10Result {
	t.Helper()
	data, err := os.ReadFile(m10GoldenPath(group))
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]m10Result
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// m10Check compares one outcome with the recorded one.
func m10Check(t *testing.T, golden map[string]m10Result, name string, source []byte, err error) {
	t.Helper()
	want, ok := golden[name]
	if !ok {
		t.Fatalf("no recorded outcome for %q", name)
	}
	got := m10Outcome(source, err)
	if got.Error != want.Error {
		t.Fatalf("error %q, want %q", got.Error, want.Error)
	}
	if !bytes.Equal([]byte(got.Source), []byte(want.Source)) {
		t.Fatalf("bytes differ\n got: %q\nwant: %q", got.Source, want.Source)
	}
}
