package edit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const catalogScore = `cicada 2
tempo 120
instrument pad {
  param brightness = 1100Hz
  voice poly {
    out = sine(pitch) * 0.1
  }
}
kit k {
  bd = model.kick
}
track bass fm_bass {}
track lead acid {}
track keys pad {}
track hits k {}
pattern p notes { c3 . }
pattern a acid steps=1 { 1 }
pattern d drums steps=1 { bd: x }
scene s { bass = p lead = a keys = p hits = d }
song { s }
`

// setparam takes every control the catalog lists for a track's voice: the
// controls of a keyboard patch, the acid's vibrato, the settings every
// instrument graph takes and the lanes of a declared kit.
func TestSetParamAcceptsEveryCatalogControl(t *testing.T) {
	for _, tc := range []struct {
		entity, value, written string
	}{
		{"param:bass.op3_level", `4`, "op3_level = 4"},
		{"param:bass.op1_attack", `5`, "op1_attack = 5ms"},
		{"param:bass.op1_attack", `"0.01s"`, "op1_attack = 0.01s"},
		{"param:bass.route4_3", `2`, "route4_3 = 2"},
		{"param:bass.sustain", `"on"`, "sustain = on"},
		{"param:bass.octave", `3`, "octave = 3"},
		{"param:lead.vibrato", `20`, "vibrato = 20ct"},
		{"param:lead.cutoff", `900`, "cutoff = 900Hz"},
		{"param:keys.octave", `3`, "octave = 3"},
		{"param:keys.glide", `"120ms"`, "glide = 120ms"},
		{"param:keys.brightness", `900`, "brightness = 900Hz"},
		{"param:hits.bd_tune", `1.5`, "bd_tune = 1.5"},
		{"param:hits.bd_level", `-3`, "bd_level = -3dB"},
	} {
		value := json.RawMessage(tc.value)
		result, err := Apply([]byte(catalogScore), Envelope{Version: 1, Intents: []Intent{&SetParam{Entity: EntityID(tc.entity), Value: value}}}, Options{Compiler: parseCompiler{}})
		if err != nil {
			t.Errorf("%s = %s: %v", tc.entity, tc.value, err)
			continue
		}
		if !strings.Contains(string(result.Source), tc.written) {
			t.Errorf("%s = %s: the score does not hold %q:\n%s", tc.entity, tc.value, tc.written, result.Source)
		}
	}
}

// A refused value names the control, the value, the range and the unit as
// fields; a control the voice does not have is unknown_setting.
func TestSetParamRefusesWithStructuredReasons(t *testing.T) {
	for _, tc := range []struct {
		entity, value, reason, field string
		rangeOf                      *ValueRange
	}{
		{"param:bass.op3_level", `9`, "value_out_of_range", "value", &ValueRange{Parameter: "op3_level", Value: "9", Min: 0, Max: 8}},
		{"param:bass.op1_attack", `"6s"`, "value_out_of_range", "value", &ValueRange{Parameter: "op1_attack", Value: "6s", Min: 0, Max: 5000, Unit: "ms"}},
		{"param:hits.bd_tune", `3`, "value_out_of_range", "value", &ValueRange{Parameter: "bd_tune", Value: "3", Min: 0.5, Max: 2}},
		{"param:lead.vibrato", `10000`, "value_out_of_range", "value", &ValueRange{Parameter: "vibrato", Value: "10000ct", Min: 0, Max: 9600, Unit: "cent"}},
		{"param:bass.op3_level", `"loud"`, "invalid_value", "value", nil},
		{"param:bass.op7_level", `1`, "unknown_setting", "entity", nil},
		{"param:hits.bd_flavor", `1`, "unknown_setting", "entity", nil},
	} {
		_, err := Apply([]byte(catalogScore), Envelope{Version: 1, Intents: []Intent{&SetParam{Entity: EntityID(tc.entity), Value: json.RawMessage(tc.value)}}}, Options{Compiler: parseCompiler{}})
		var reason *ReasonError
		if !errors.As(err, &reason) {
			t.Errorf("%s = %s: want reason %s, got %v", tc.entity, tc.value, tc.reason, err)
			continue
		}
		if reason.Reason != tc.reason || reason.Field != tc.field {
			t.Errorf("%s = %s: reason %s field %s, want %s %s (%s)", tc.entity, tc.value, reason.Reason, reason.Field, tc.reason, tc.field, reason.Message)
		}
		if tc.rangeOf != nil && (reason.Range == nil || *reason.Range != *tc.rangeOf) {
			t.Errorf("%s = %s: range %+v, want %+v", tc.entity, tc.value, reason.Range, tc.rangeOf)
		}
		var located *IntentError
		if !errors.As(err, &located) || located.Field != tc.field {
			t.Errorf("%s = %s: the intent error does not name field %s: %v", tc.entity, tc.value, tc.field, err)
		}
	}
}
