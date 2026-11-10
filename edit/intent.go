package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
		return nil, &FieldError{Field: "kind", Err: fmt.Errorf("intent needs a kind")}
	}
	make, ok := registry[kind]
	if !ok {
		return nil, &FieldError{Field: "kind", Err: fmt.Errorf("unknown intent kind %q", kind)}
	}
	delete(fields, "kind")
	body, _ := json.Marshal(fields)
	intent := make()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(intent); err != nil {
		return nil, &FieldError{Field: decodeField(err), Err: fmt.Errorf("%s: %w", kind, err)}
	}
	// A kind that refuses some fields of a struct it shares refuses them here,
	// as the decoder refuses an unknown field.
	for _, path := range kindRules[kind].exclude {
		if jsonPathPresent(fields, path) {
			name := path[strings.LastIndex(path, ".")+1:]
			return nil, &FieldError{Field: path, Err: fmt.Errorf("%s: json: unknown field %q", kind, name)}
		}
	}
	return intent, nil
}

// jsonPathPresent reports whether the dotted path names a field of the
// decoded object fields.
func jsonPathPresent(fields map[string]json.RawMessage, path string) bool {
	head, rest, nested := strings.Cut(path, ".")
	raw, ok := fields[head]
	if !ok {
		return false
	}
	if !nested {
		return true
	}
	var inner map[string]json.RawMessage
	if json.Unmarshal(raw, &inner) != nil {
		return false
	}
	return jsonPathPresent(inner, rest)
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
