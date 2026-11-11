package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/testwav"
)

// catalogFixture names the fixture score in testdata/intent-catalog that an
// intent's catalog example edits. Most examples edit song.cicada.
func catalogFixture(kind string) string {
	switch kind {
	case "addmarker", "addplacement", "copyplacement", "deleteclip", "duplicateclip", "moveclip",
		"placesamplerprint", "removearrangementtempo", "removemarker", "removeplacement", "resizeclip",
		"setarrangementtempo", "setmarker", "splitclip", "updateplacement", "varyplacement":
		return "arrange.cicada"
	case "convertsongtoarrange":
		// The conversion refuses scenes with settings, which song.cicada has.
		return "convert.cicada"
	}
	return "song.cicada"
}

// catalogProject copies an intent's fixture score into a new folder with the
// audio files and library pins its example needs, and returns the score.
func catalogProject(t *testing.T, kind string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "intent-catalog", catalogFixture(kind)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	score := filepath.Join(dir, "catalog.cicada")
	if err := os.WriteFile(score, source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "audio"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The examples that print or record audio name these files and their
	// SHA-256: example.wav (mono, also song.cicada's asset) and print.wav
	// (stereo, as a sampler print is).
	for name, channels := range map[string]int{"example.wav": 1, "print.wav": 2} {
		if err := os.WriteFile(filepath.Join(dir, "audio", name), testwav.Bytes(48000, channels, 16, 4800, 1), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "insertlibraryitem" {
		// cicada apply does not pin libraries (Studio pins them before it
		// applies the intent), so the project pins std/synth first, as the
		// intent's notes tell a caller to.
		_, pins, err := studioLibraryPins(score, edits.LibraryImport(source, "std/synth"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pins.Path, pins.After, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return score
}

// applyCatalogPayload runs one intent through cicada apply as a dry run and
// returns the diff.
func applyCatalogPayload(t *testing.T, score string, intent []byte) (string, error) {
	t.Helper()
	envelope := filepath.Join(filepath.Dir(score), "intents.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+string(intent)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := applyCommandWithHooks([]string{score, envelope}, &stdout, nil)
	return stdout.String(), err
}

// TestIntentCatalogExamplesApply runs every catalog example through cicada
// apply, as a dry run, against the fixture scores in testdata/intent-catalog,
// and requires a change. A new intent must bring an example that applies.
func TestIntentCatalogExamplesApply(t *testing.T) {
	for _, info := range edits.Catalog() {
		t.Run(info.Kind, func(t *testing.T) {
			score := catalogProject(t, info.Kind)
			for _, example := range info.Examples {
				diff, err := applyCatalogPayload(t, score, example)
				if err != nil {
					t.Errorf("example %s does not apply to %s: %v", example, catalogFixture(info.Kind), err)
				} else if !strings.Contains(diff, "@@") {
					t.Errorf("example %s changes nothing in %s", example, catalogFixture(info.Kind))
				}
			}
		})
	}
}

// catalogLeaf is one field the example sets, with what the catalog says of it.
type catalogLeaf struct {
	path     []any // object keys and array indexes from the intent's top level
	field    edits.IntentField
	required bool // required, or required_when holds in the example
}

func (leaf catalogLeaf) name() string {
	var parts []string
	for _, step := range leaf.path {
		switch step := step.(type) {
		case string:
			parts = append(parts, step)
		case int:
			parts[len(parts)-1] += fmt.Sprintf("[%d]", step)
		}
	}
	return strings.Join(parts, ".")
}

// catalogLeaves lists every field the example sets, nested objects and the
// first element of each list of objects included.
func catalogLeaves(t *testing.T, info edits.IntentInfo, raw json.RawMessage) []catalogLeaf {
	t.Helper()
	var example map[string]any
	if err := json.Unmarshal(raw, &example); err != nil {
		t.Fatal(err)
	}
	var leaves []catalogLeaf
	var walk func(object map[string]any, fields []edits.IntentField, path []any)
	walk = func(object map[string]any, fields []edits.IntentField, path []any) {
		for _, field := range fields {
			value, ok := object[field.Name]
			if !ok {
				if field.Required {
					t.Errorf("the example lacks %v.%s, which the catalog marks required", path, field.Name)
				}
				continue
			}
			at := append(append([]any(nil), path...), field.Name)
			required := field.Required || conditionHolds(example, field.RequiredWhen)
			leaves = append(leaves, catalogLeaf{path: at, field: field, required: required})
			switch value := value.(type) {
			case map[string]any:
				walk(value, field.Fields, at)
			case []any:
				if len(value) > 0 {
					if element, ok := value[0].(map[string]any); ok {
						walk(element, field.Fields, append(at, 0))
					}
				}
			}
		}
	}
	walk(example, info.Fields, nil)
	return leaves
}

// conditionHolds reports whether an example meets a required_when or one_of
// condition; a nil condition always holds.
func conditionHolds(example any, when *edits.FieldCondition) bool {
	if when == nil {
		return false
	}
	other, ok := lookupJSON(example, strings.Split(when.Field, "."))
	if !ok {
		return false
	}
	if when.Present {
		return true
	}
	for _, want := range when.Values {
		if fmt.Sprint(other) == want {
			return true
		}
	}
	return false
}

// dottedName drops list indexes from a leaf name: notes[0].at is notes.at.
func dottedName(name string) string {
	return regexp.MustCompile(`\[\d+\]`).ReplaceAllString(name, "")
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func lookupJSON(value any, path []string) (any, bool) {
	for _, key := range path {
		if list, ok := value.([]any); ok && len(list) > 0 {
			value = list[0]
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		if value, ok = object[key]; !ok {
			return nil, false
		}
	}
	return value, true
}

// editedExample returns the example with the value at path removed, or
// replaced when replace is not nil.
func editedExample(t *testing.T, raw json.RawMessage, path []any, replace any) []byte {
	t.Helper()
	var example any
	if err := json.Unmarshal(raw, &example); err != nil {
		t.Fatal(err)
	}
	parent := example
	for _, step := range path[:len(path)-1] {
		switch step := step.(type) {
		case string:
			parent = parent.(map[string]any)[step]
		case int:
			parent = parent.([]any)[step]
		}
	}
	key := path[len(path)-1].(string)
	if replace == nil {
		delete(parent.(map[string]any), key)
	} else {
		parent.(map[string]any)[key] = replace
	}
	data, err := json.Marshal(example)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestIntentCatalogFieldsAreTrue checks what intents --json says of each
// field against cicada apply, for every kind: an example without a field the
// catalog marks required (or required by a required_when the example meets)
// is refused; an example without an optional field still applies; a string
// outside a field's values is refused; and an example without every field of
// a one_of group is refused. A field the example leaves out is optional by
// the example itself.
func TestIntentCatalogFieldsAreTrue(t *testing.T) {
	for _, info := range edits.Catalog() {
		t.Run(info.Kind, func(t *testing.T) {
			score := catalogProject(t, info.Kind)
			for _, example := range info.Examples {
				checkCatalogExample(t, info, score, example)
			}
		})
	}
}

// checkCatalogExample runs the required, optional, values and one_of checks
// of TestIntentCatalogFieldsAreTrue on one example.
func checkCatalogExample(t *testing.T, info edits.IntentInfo, score string, example json.RawMessage) {
	t.Helper()
	leaves := catalogLeaves(t, info, example)
	var parsed any
	if err := json.Unmarshal(example, &parsed); err != nil {
		t.Fatal(err)
	}
	// A field that is the example's only member of a one_of group that holds
	// is required there: removing it empties the group.
	for _, group := range info.OneOf {
		if group.When != nil && !conditionHolds(parsed, group.When) {
			continue
		}
		var present []int
		for i, leaf := range leaves {
			if containsName(group.Fields, dottedName(leaf.name())) {
				present = append(present, i)
			}
		}
		if len(present) == 1 {
			leaves[present[0]].required = true
		}
	}
	for _, leaf := range leaves {
		_, err := applyCatalogPayload(t, score, editedExample(t, example, leaf.path, nil))
		if leaf.required && err == nil {
			t.Errorf("%s: %s is marked required, but the example applies without it", example, leaf.name())
		}
		if !leaf.required && err != nil {
			t.Errorf("%s: %s is marked optional, but the example without it is refused: %v", example, leaf.name(), err)
		}
		if len(leaf.field.Values) > 0 && leaf.field.Type == "string" {
			if _, err := applyCatalogPayload(t, score, editedExample(t, example, leaf.path, "not-a-listed-value")); err == nil {
				t.Errorf("%s: %s accepts a value outside its list %v", example, leaf.name(), leaf.field.Values)
			}
		}
	}
	for _, group := range info.OneOf {
		if group.When != nil && !conditionHolds(parsed, group.When) {
			continue
		}
		var without any
		if err := json.Unmarshal(example, &without); err != nil {
			t.Fatal(err)
		}
		present := false
		for _, path := range group.Fields {
			parts := strings.Split(path, ".")
			if parent, ok := lookupJSON(without, parts[:len(parts)-1]); ok {
				if object, ok := parent.(map[string]any); ok {
					_, has := object[parts[len(parts)-1]]
					present = present || has
					delete(object, parts[len(parts)-1])
				}
			}
		}
		data, _ := json.Marshal(without)
		if _, err := applyCatalogPayload(t, score, data); present && err == nil {
			t.Errorf("%s applies without any of one_of %v", example, group.Fields)
		}
	}
}

func TestIntentsCommandJSON(t *testing.T) {
	var stdout bytes.Buffer
	if err := intentsCommand([]string{"--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schema          string `json:"schema"`
		EnvelopeVersion int    `json:"envelope_version"`
		Envelope        []struct {
			Name string `json:"name"`
		} `json:"envelope"`
		Intents []struct {
			Kind    string          `json:"kind"`
			Meaning string          `json:"meaning"`
			Example json.RawMessage `json:"example"`
			Fields  []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"intents"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("intents --json is not JSON: %v", err)
	}
	if document.Schema != intentsSchema || document.EnvelopeVersion != edits.EnvelopeVersion || len(document.Envelope) == 0 {
		t.Fatalf("header: %+v", document)
	}
	if len(document.Intents) != len(edits.Kinds()) {
		t.Fatalf("catalog lists %d intents; the registry has %d", len(document.Intents), len(edits.Kinds()))
	}
	for i, intent := range document.Intents {
		if i > 0 && document.Intents[i-1].Kind >= intent.Kind {
			t.Fatalf("intents are not sorted by kind at %s", intent.Kind)
		}
		if intent.Meaning == "" || len(intent.Example) == 0 {
			t.Errorf("%s lacks a meaning or an example", intent.Kind)
		}
	}
	var one bytes.Buffer
	if err := intentsCommand([]string{"setstep"}, &one); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"setstep: ", "Fields:", "entity (entity, required)", "Example:"} {
		if !strings.Contains(one.String(), want) {
			t.Fatalf("cicada intents setstep lacks %q:\n%s", want, one.String())
		}
	}
	if err := intentsCommand([]string{"nosuchkind"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown intent kind") {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestDiagnosticsCommand(t *testing.T) {
	var stdout bytes.Buffer
	if err := diagnosticsCommand([]string{"--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schema      string `json:"schema"`
		Diagnostics []struct {
			Code       string   `json:"code"`
			Severities []string `json:"severities"`
			Meaning    string   `json:"meaning"`
			Fix        string   `json:"fix"`
			Reasons    []struct {
				Reason string `json:"reason"`
			} `json:"reasons"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("diagnostics --json is not JSON: %v", err)
	}
	if document.Schema != diagnosticsSchema || len(document.Diagnostics) < 70 {
		t.Fatalf("schema %q, %d codes", document.Schema, len(document.Diagnostics))
	}
	reasons := 0
	for _, entry := range document.Diagnostics {
		if entry.Meaning == "" || entry.Fix == "" || len(entry.Severities) == 0 {
			t.Errorf("%s is incomplete", entry.Code)
		}
		if entry.Code == "CICADA-NOTES" {
			reasons = len(entry.Reasons)
		}
	}
	if reasons != len(edits.NoteErrorCodes) {
		t.Fatalf("CICADA-NOTES lists %d reasons; the editor has %d", reasons, len(edits.NoteErrorCodes))
	}
	stdout.Reset()
	if err := diagnosticsCommand([]string{"slide-rest"}, &stdout); err != nil || !strings.Contains(stdout.String(), "CICADA-SLIDE-REST (warning; reported by: score)") || !strings.Contains(stdout.String(), "Fix: ") {
		t.Fatalf("diagnostics slide-rest: %v\n%s", err, stdout.String())
	}
	if err := diagnosticsCommand([]string{"CICADA-NOPE"}, &bytes.Buffer{}); err == nil {
		t.Fatal("an unknown code was accepted")
	}
}
