package edit

import "fmt"

// These declarations match pull request #319 (branch
// add/agent-surface-e3-20261110, edit/effects.go and edit/mixer.go), which
// adds them for effect intents. Whichever of the two merges second deletes
// this file.

// ReasonError is an intent refused for a reason a program can act on: Reason
// is a stable snake_case code, Field the JSON field at fault (empty when the
// whole intent is), and Uses lists what still refers to the entity when that
// is the reason. File names the other source file that holds what the edit
// would have to change, and Range the bounds a value broke. Error returns
// Message.
type ReasonError struct {
	Reason  string
	Field   string
	Message string
	Uses    []string
	File    string
	Range   *ValueRange
}

// ValueRange is a parameter's accepted range and the value given outside it.
type ValueRange struct {
	Parameter string  `json:"parameter"`
	Value     string  `json:"value"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Unit      string  `json:"unit,omitempty"`
}

func (e *ReasonError) Error() string { return e.Message }

func refuse(reason, field, format string, args ...any) error {
	return &ReasonError{Reason: reason, Field: field, Message: fmt.Sprintf(format, args...)}
}

// invalidValue refuses a value that is not of the parameter's form, with
// reason invalid_value. Its field is the intent's value; a caller that knows
// a better one wraps it.
func invalidValue(format string, args ...any) error {
	return &ReasonError{Reason: "invalid_value", Field: "value", Message: fmt.Sprintf(format, args...)}
}

// outOfRange refuses a value outside a parameter's range with reason
// value_out_of_range, the parameter, the value given, the range and the unit.
// Its field is the intent's value; a caller that knows a better one wraps it.
func outOfRange(parameter, value string, min, max float64, unit, message string) error {
	return &ReasonError{Reason: "value_out_of_range", Field: "value", Message: message, Range: &ValueRange{Parameter: parameter, Value: value, Min: min, Max: max, Unit: unit}}
}
