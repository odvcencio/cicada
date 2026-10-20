package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
)

const builtinSoundScore = `cicada 1
title "Sound edits keep this title"
track bass acid { cutoff = 723.25Hz } // keep this annotation
track beat drums { bd_decay = 400ms }
pattern p acid { 1 . 5 . }
pattern d drums { bd: x . . . }
scene main { bass=p beat=d }
song { main*2 }
`

func TestBuiltinSoundSavedActionsUseCatalogAndExactUndo(t *testing.T) {
	for _, c := range []struct{ track, param, value, want string }{
		{"bass", "cutoff", "1250.125", "cutoff = 1250.125Hz"},
		{"bass", "decay", "0.75s", "decay = 0.75s"},
		{"bass", "filter", "ladder", "filter = ladder"},
		{"bass", "savage", "on", "savage = on"},
		{"bass", "octave", "3", "octave = 3"},
		{"beat", "bd_tune", "65.25", "bd_tune = 65.25Hz"},
		{"beat", "bd_decay", "825.125", "bd_decay = 825.125ms"},
		{"beat", "ch_metal", "on", "ch_metal = on"},
	} {
		t.Run(c.track+"."+c.param, func(t *testing.T) {
			before := []byte(builtinSoundScore)
			path := studioTestPath(t, builtinSoundScore)
			handler, err := studioHandler(path)
			if err != nil {
				t.Fatal(err)
			}
			value, _ := json.Marshal(c.value)
			edit := studioEdit{Revision: studioRevision(before), Action: "set-parameter", Track: c.track, NewName: c.param, Value: value}
			response := studioCall(t, handler, "/api/instrument", edit)
			if response.Code != 200 {
				t.Fatalf("saved built-in parameter: %d %s", response.Code, response.Body.String())
			}
			updated, _ := os.ReadFile(path)
			if !bytes.Contains(updated, []byte(c.want)) || !bytes.Contains(updated, []byte("// keep this annotation")) {
				t.Fatalf("source lost value or annotation:\n%s", updated)
			}
			if stale := studioCall(t, handler, "/api/instrument", edit); stale.Code != 409 {
				t.Fatalf("stale built-in edit: %d", stale.Code)
			}
			edit.Action, edit.Revision, edit.Value = "reset-parameter", studioRevision(updated), nil
			response = studioCall(t, handler, "/api/instrument", edit)
			if response.Code != 200 {
				t.Fatalf("reset built-in parameter: %d %s", response.Code, response.Body.String())
			}
			reset, _ := os.ReadFile(path)
			score, diagnostics := notation.Parse(reset)
			if score == nil || hasDiagnosticErrors(diagnostics) {
				t.Fatalf("reset invalidated source: %+v", diagnostics)
			}
			for _, track := range score.Tracks {
				if track.Name == c.track {
					for _, parameter := range track.Params {
						if parameter.Name == c.param {
							t.Fatal("Use default left the source override")
						}
					}
				}
			}
			if undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(reset)}); undo.Code != 200 {
				t.Fatalf("Undo reset: %d %s", undo.Code, undo.Body.String())
			}
			restored, _ := os.ReadFile(path)
			if !bytes.Equal(restored, updated) {
				t.Fatal("Undo reset did not restore the exact overridden source")
			}
			if undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(restored)}); undo.Code != 200 {
				t.Fatalf("Undo setting: %d %s", undo.Code, undo.Body.String())
			}
			restored, _ = os.ReadFile(path)
			if !bytes.Equal(restored, before) {
				t.Fatalf("Undo changed exact source:\n%s", restored)
			}
		})
	}
}

func TestBuiltinSoundInvalidSavedActionsNeverChangeSource(t *testing.T) {
	path := studioTestPath(t, builtinSoundScore)
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ track, param, value string }{
		{"bass", "cutoff", "10ms"}, {"bass", "cutoff", "NaNHz"}, {"bass", "cutoff", "9000Hz"},
		{"bass", "filter", "random"}, {"bass", "savage", "maybe"}, {"bass", "level", "-6dB"},
		{"beat", "bd_tune", "200Hz"}, {"beat", "ch_metal", "maybe"}, {"beat", "nope", "1"},
	} {
		value, _ := json.Marshal(c.value)
		response := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision([]byte(builtinSoundScore)), Action: "set-parameter", Track: c.track, NewName: c.param, Value: value})
		if response.Code != 422 {
			t.Fatalf("invalid %s.%s %s: %d %s", c.track, c.param, c.value, response.Code, response.Body.String())
		}
		unchanged, _ := os.ReadFile(path)
		if string(unchanged) != builtinSoundScore {
			t.Fatal("invalid Sound control changed disk")
		}
	}
}

func TestBuiltinKeyboardToggleUsesPatchCatalogAndUndo(t *testing.T) {
	source := "cicada 2\ntrack keys tonewheel_organ {}\npattern p notes { c4 . }\nscene main { keys=p }\nsong { main }\n"
	path := studioTestPath(t, source)
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision([]byte(source)), Action: "set-parameter", Track: "keys", NewName: "rotary_fast", Value: json.RawMessage(`"fast"`)})
	if response.Code != 200 {
		t.Fatalf("saved keyboard choice: %d %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if !bytes.Contains(updated, []byte("rotary_fast = fast")) {
		t.Fatal("keyboard choice did not persist")
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(updated)})
	restored, _ := os.ReadFile(path)
	if undo.Code != 200 || string(restored) != source {
		t.Fatal("keyboard choice did not Undo exactly")
	}
}
