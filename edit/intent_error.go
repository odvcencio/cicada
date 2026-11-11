package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// IntentError says which intent of a batch failed: its index in the
// envelope's intents (counted from 0), its kind and, when the code knows it,
// the JSON field at fault. Error returns the cause's message unchanged, so
// hosts that show it read as before; Location names the intent for a caller
// that wants to point at it.
type IntentError struct {
	Index int
	Kind  string
	Field string
	Err   error
}

func (e *IntentError) Error() string { return e.Err.Error() }
func (e *IntentError) Unwrap() error { return e.Err }

// Location is intents[<index>] or intents[<index>].<field>, then the kind,
// such as `intents[1].pitch (setstep)`.
func (e *IntentError) Location() string {
	at := fmt.Sprintf("intents[%d]", e.Index)
	if e.Field != "" {
		at += "." + e.Field
	}
	if e.Kind != "" {
		at += " (" + e.Kind + ")"
	}
	return at
}

// FieldError names the JSON field of an intent whose value an edit refuses.
// Error returns the cause's message unchanged.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// entityError marks an error about an intent's entity field.
func entityError(err error) error {
	if err == nil {
		return nil
	}
	return &FieldError{Field: "entity", Err: err}
}

// intentError wraps a handler's error with the intent's place in the batch.
func intentError(index int, kind string, err error) error {
	located := &IntentError{Index: index, Kind: kind, Err: err}
	var field *FieldError
	var reason *ReasonError
	if errors.As(err, &field) {
		located.Field = field.Field
	} else if errors.As(err, &reason) {
		located.Field = reason.Field
	}
	return located
}

// decodeField names the JSON field a decoding error is about: the path of a
// value of the wrong type, or an unknown field.
func decodeField(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return typeErr.Field
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return strings.Trim(name, `"`)
	}
	return ""
}
