package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Intent is one typed edit. Kind is the lowercase registry key carried as
// "kind" in JSON. Every intent type has its own Kind and lowercase JSON tags.
type Intent interface{ Kind() string }

var registry = map[string]func() Intent{}

// Register adds an intent constructor; a duplicate kind panics at init.
func Register(kind string, make func() Intent) {
	if _, dup := registry[kind]; dup {
		panic("edit: duplicate intent kind " + kind)
	}
	registry[kind] = make
}

// Kinds lists registered kinds in sorted order (for docs and tests).
func Kinds() []string {
	kinds := make([]string, 0, len(registry))
	for kind := range registry {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func decodeIntent(raw json.RawMessage) (Intent, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var kind string
	if err := json.Unmarshal(fields["kind"], &kind); err != nil || kind == "" {
		return nil, fmt.Errorf("intent needs a kind")
	}
	make, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown intent kind %q", kind)
	}
	delete(fields, "kind")
	body, _ := json.Marshal(fields)
	intent := make()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(intent); err != nil {
		return nil, fmt.Errorf("%s: %w", kind, err)
	}
	return intent, nil
}

func encodeIntent(intent Intent) (json.RawMessage, error) {
	body, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	fields["kind"], _ = json.Marshal(intent.Kind())
	return json.Marshal(fields) // map keys marshal in sorted order, so "kind" sits among the other keys alphabetically
}
