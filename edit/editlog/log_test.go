package editlog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"m31labs.dev/cicada/edit"
)

func TestAppendAndReadLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := Path(filepath.Join(dir, "main.cicada"))
	if path != filepath.Join(dir, ".cicada", "edits.jsonl") {
		t.Fatal(path)
	}
	env := edit.Envelope{Version: 1, Author: "tester", Session: "s1", Intents: []edit.Intent{&edit.SetParam{Entity: "param:keys.cutoff", Value: json.RawMessage(`1`)}}}
	record, err := Commit(env, "before", "after", "Set keys.cutoff", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendCommit(path, record); err != nil {
		t.Fatal(err)
	}
	if err := AppendCommit(path, record); err != nil {
		t.Fatal(err)
	}
	records, _, err := ReadLog(path)
	if err != nil || len(records) != 2 || records[1].Revision != "after" || records[1].Parent != "before" || records[1].Author != "tester" || records[1].Session != "s1" || len(records[1].Intents) != 1 {
		t.Fatalf("%+v %v", records, err)
	}
	data, _ := os.ReadFile(path)
	if lines := bytes.Count(data, []byte("\n")); lines != 2 {
		t.Fatalf("%d lines: %s", lines, data)
	}
	if missing, _, err := ReadLog(filepath.Join(dir, "none.jsonl")); err != nil || missing != nil {
		t.Fatal(err)
	}
}

func TestTornLineDoesNotPoisonLaterRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cicada", "edits.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"revision":"torn","par`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"a", "b", "c"} {
		record, err := Commit(edit.Envelope{Version: 1}, "p", revision, "", time.Unix(0, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := AppendCommit(path, record); err != nil {
			t.Fatal(err)
		}
	}
	records, skipped, err := ReadLog(path)
	if err != nil || skipped != 1 || len(records) != 3 || records[0].Revision != "a" || records[2].Revision != "c" {
		t.Fatalf("%+v skipped=%d %v", records, skipped, err)
	}
}
