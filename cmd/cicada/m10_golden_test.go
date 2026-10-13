package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// m10Recorder writes outcomes of the retired writers when RECORD_M10_GOLDEN is
// set. It exists only while the writers do.
type m10Recorder struct {
	mu      sync.Mutex
	results map[string]m10Result
	calls   map[string]int
}

func newM10Recorder(t *testing.T, group string) *m10Recorder {
	r := &m10Recorder{results: map[string]m10Result{}, calls: map[string]int{}}
	if os.Getenv("RECORD_M10_GOLDEN") != "" {
		t.Cleanup(func() {
			data, err := json.MarshalIndent(r.results, "", "  ")
			if err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(m10GoldenPath(group), append(data, '\n'), 0o644); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}

func (r *m10Recorder) add(name string, source []byte, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results[name] = m10Outcome(source, err)
}

// addNext records the nth outcome of a test that runs several edits in a row.
func (r *m10Recorder) addNext(name string, source []byte, err error) string {
	r.mu.Lock()
	r.calls[name]++
	key := fmt.Sprintf("%s#%d", name, r.calls[name])
	r.mu.Unlock()
	r.add(key, source, err)
	return key
}
