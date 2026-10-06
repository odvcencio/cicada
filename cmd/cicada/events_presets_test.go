package main

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestEventsResolvePresets(t *testing.T) {
	for _, test := range []struct{ name, declaration, target, settings, pattern string }{
		{"authored", "instrument voice { octave=4 voice mono { out=saw(pitch)*env(gate, 90ms) } }\n", "voice", "level=-9dB", "pattern melody { 1 . }\n"},
		{"acid", "", "acid", "octave=4 gate=70", "pattern melody { 1 . }\n"},
		{"drums", "", "drums", "bd_tune=52Hz", "pattern melody drums { bd: X... }\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := func(source string) []eventRecord {
				t.Helper()
				score, ds := notation.Parse([]byte(source + test.pattern + "scene main { lead=melody }\nsong { main }\n"))
				if score == nil || hasDiagnosticErrors(ds) {
					t.Fatalf("parse: %+v", ds)
				}
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				stdout := os.Stdout
				os.Stdout = w
				err = emitEvents(score, "lead", "melody")
				os.Stdout = stdout
				w.Close()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				var records []eventRecord
				if err := json.Unmarshal(data, &records); err != nil {
					t.Fatal(err)
				}
				return records
			}
			inline := events(test.declaration + "track lead " + test.target + " { " + test.settings + " }\n")
			preset := events(test.declaration + "preset saved { instrument=" + test.target + " " + test.settings + " }\ntrack lead saved {}\n")
			if len(inline) == 0 || !reflect.DeepEqual(inline, preset) {
				t.Fatalf("events differ: inline=%+v preset=%+v", inline, preset)
			}
		})
	}
}
