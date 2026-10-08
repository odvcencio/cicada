// Package editlog appends one JSON line per commit to .cicada/edits.jsonl
// (spec 7.6 item 1). History, collaboration and agents read it.
package editlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"m31labs.dev/cicada/edit"
)

// Record is one committed edit.
type Record struct {
	Revision string            `json:"revision"`
	Parent   string            `json:"parent"`
	Author   string            `json:"author"`
	Session  string            `json:"session"`
	At       time.Time         `json:"at"`
	Label    string            `json:"label,omitempty"`
	Intents  []json.RawMessage `json:"intents"`
}

// Path is the log file beside the score.
func Path(scorePath string) string {
	return filepath.Join(filepath.Dir(scorePath), ".cicada", "edits.jsonl")
}

// Commit builds the record for one envelope that took parent to revision.
func Commit(env edit.Envelope, parent, revision, label string, at time.Time) (Record, error) {
	raw, err := env.RawIntents()
	if err != nil {
		return Record{}, err
	}
	if raw == nil {
		raw = []json.RawMessage{}
	}
	return Record{Revision: revision, Parent: parent, Author: env.Author, Session: env.Session, At: at, Label: label, Intents: raw}, nil
}

// AppendCommit appends record as one line and syncs the file.
func AppendCommit(path string, record Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	// A crash can leave a torn last line. Start a new line so it cannot
	// swallow this record.
	if info, statErr := file.Stat(); statErr == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, readErr := file.ReadAt(last, info.Size()-1); readErr == nil && last[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err = file.Write(append(line, '\n')); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// ReadLog reads every well-formed record. A line that does not parse (for
// example one torn by a crash) is skipped and counted; a missing file yields
// nil. An error is returned only when the file cannot be read.
func ReadLog(path string) (records []Record, skipped int, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record Record
		if json.Unmarshal(line, &record) != nil {
			skipped++
			continue
		}
		records = append(records, record)
	}
	return records, skipped, nil
}
