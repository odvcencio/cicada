package edit_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

// Exact bytes and errors captured from the old writers before the port.
func TestPatternExactBytesAndErrors(t *testing.T) {
	data, err := os.ReadFile("../cmd/cicada/testdata/pattern-intent-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Source, Intent, Result, Error, Label string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := applyGridJSON(t, []byte(c.Source), c.Intent)
			if c.Error != "" {
				if err == nil || err.Error() != c.Error {
					t.Fatalf("error %v, want %q", err, c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Source) != c.Result || got.Label != c.Label {
				t.Fatalf("bytes/label %q / %q, want %q / %q", got.Source, got.Label, c.Result, c.Label)
			}
			if strings.Contains(c.Name, "comment duplicate") && (commentAnchor([]byte(c.Source), "// keep me") != "pulse" || commentAnchor(got.Source, "// keep me") != "pulse") {
				t.Fatal("comment left original pattern")
			}
		})
	}
}

func TestPatternSharedPolicies(t *testing.T) {
	source := []byte("track bass acid {}\nphrase riff { c3 . }\npattern pulse acid { 1 . use riff +12 }\nscene main { bass=pulse }\nsong { main }\n")
	for _, in := range []edit.Intent{
		&edit.SetStep{Entity: "step:pulse/2", Mode: "note", Pitch: 62, Ratchet: 1, Chance: 100},
		&edit.SetRange{Entity: "pattern:pulse", Operation: "clear", First: 0, Last: 3},
		&edit.ResizePattern{Entity: "pattern:pulse", Length: 8},
	} {
		_, err := edit.Apply(source, edit.Envelope{Version: 1, Intents: []edit.Intent{in}}, edit.Options{Compiler: gridCompiler{}})
		var shared *edit.SharedPhraseError
		if !errors.As(err, &shared) || shared.Step != 2 || shared.Phrase != "riff" {
			t.Fatalf("%s must ask: %v", in.Kind(), err)
		}
	}
	got, err := edit.Apply(source, edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.SetStep{Entity: "step:pulse/2", Mode: "note", Pitch: 62, Ratchet: 1, Chance: 100, Shared: "definition"}}}, edit.Options{Compiler: gridCompiler{}})
	want := strings.Replace(string(source), "phrase riff { c3 . }", "phrase riff { d3 . }", 1)
	if err != nil || string(got.Source) != want {
		t.Fatalf("definition lost use transpose: %+v, %v", got, err)
	}
}

func commentAnchor(source []byte, comment string) string {
	at := strings.Index(string(source), comment)
	if at < 0 {
		return ""
	}
	line := string(source[:at])
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	fields := strings.Fields(line)
	if len(fields) > 1 && fields[0] == "pattern" {
		return fields[1]
	}
	return ""
}

func TestPatternIntentBatchUsesUpdatedPlan(t *testing.T) {
	// The second pitch click must see the first edit's compiled note.
	env := edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.SetPitch{Entity: "step:pulse/1", Pitch: 60}, &edit.SetPitch{Entity: "step:pulse/1", Pitch: 60}}}
	got, err := edit.Apply([]byte(gridScore), env, edit.Options{Compiler: gridCompiler{}})
	if err != nil || string(got.Source) != gridScore {
		t.Fatalf("batch kept a stale plan: %+v, %v", got, err)
	}
}
