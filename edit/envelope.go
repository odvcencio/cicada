package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const EnvelopeVersion = 1

// Envelope is the versioned, attributed carrier of intents (spec 7.1, 7.6).
type Envelope struct {
	Version  int
	Revision string // revision the intents were computed against; "" skips the check in Apply (cicada apply fills it)
	Author   string
	Session  string
	DryRun   bool
	Intents  []Intent
	// Revisions names, for other source files of the project, the revision
	// each was at when the intents were computed (cicada apply --json reports
	// them as file_revisions). The host refuses the batch when a listed file
	// has changed since; nil checks none.
	Revisions []FileRevision
}

// FileRevision is a source file's revision, by its path as cicada apply
// --json writes paths.
type FileRevision struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type envelopeWire struct {
	Version   int               `json:"version"`
	Revision  string            `json:"revision,omitempty"`
	Author    string            `json:"author,omitempty"`
	Session   string            `json:"session,omitempty"`
	DryRun    bool              `json:"dryrun,omitempty"`
	Revisions []FileRevision    `json:"revisions,omitempty"`
	Intents   []json.RawMessage `json:"intents"`
}

func (e Envelope) MarshalJSON() ([]byte, error) {
	raw, err := e.RawIntents()
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelopeWire{Version: e.Version, Revision: e.Revision, Author: e.Author, Session: e.Session, DryRun: e.DryRun, Revisions: e.Revisions, Intents: raw})
}

func (e *Envelope) UnmarshalJSON(data []byte) error {
	var wire envelopeWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.Version != EnvelopeVersion {
		return fmt.Errorf("unsupported envelope version %d", wire.Version)
	}
	intents := make([]Intent, 0, len(wire.Intents))
	for index, raw := range wire.Intents {
		intent, err := decodeIntent(raw)
		if err != nil {
			var kind struct {
				Kind string `json:"kind"`
			}
			json.Unmarshal(raw, &kind)
			if _, known := registry[kind.Kind]; !known {
				kind.Kind = ""
			}
			return intentError(index, kind.Kind, err)
		}
		intents = append(intents, intent)
	}
	*e = Envelope{Version: wire.Version, Revision: wire.Revision, Author: wire.Author, Session: wire.Session, DryRun: wire.DryRun, Revisions: wire.Revisions, Intents: intents}
	return nil
}

// RawIntents encodes the intents for the edit log.
func (e Envelope) RawIntents() ([]json.RawMessage, error) {
	raw := make([]json.RawMessage, 0, len(e.Intents))
	for _, intent := range e.Intents {
		body, err := encodeIntent(intent)
		if err != nil {
			return nil, err
		}
		raw = append(raw, body)
	}
	return raw, nil
}
