package edit

import (
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
}

type envelopeWire struct {
	Version  int               `json:"version"`
	Revision string            `json:"revision,omitempty"`
	Author   string            `json:"author,omitempty"`
	Session  string            `json:"session,omitempty"`
	DryRun   bool              `json:"dryrun,omitempty"`
	Intents  []json.RawMessage `json:"intents"`
}

func (e Envelope) MarshalJSON() ([]byte, error) {
	raw, err := e.RawIntents()
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelopeWire{Version: e.Version, Revision: e.Revision, Author: e.Author, Session: e.Session, DryRun: e.DryRun, Intents: raw})
}

func (e *Envelope) UnmarshalJSON(data []byte) error {
	var wire envelopeWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Version != EnvelopeVersion {
		return fmt.Errorf("unsupported envelope version %d", wire.Version)
	}
	intents := make([]Intent, 0, len(wire.Intents))
	for _, raw := range wire.Intents {
		intent, err := decodeIntent(raw)
		if err != nil {
			return err
		}
		intents = append(intents, intent)
	}
	*e = Envelope{Version: wire.Version, Revision: wire.Revision, Author: wire.Author, Session: wire.Session, DryRun: wire.DryRun, Intents: intents}
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
