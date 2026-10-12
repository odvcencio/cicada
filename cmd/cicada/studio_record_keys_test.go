package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func recordedKeysSource(kind string) []byte {
	return []byte(fmt.Sprintf("cicada 2\ntrack part %s { voices=2 } // keep controls\npattern take notes steps=4 { . . . . }\nscene main { part=take }\nsong { main }\n", kind))
}

func TestRecordedKeysRejectUnsupportedExpressionAndRangeAtomically(t *testing.T) {
	for _, kind := range []string{"tine_bell", "organ_jazz", "fm_bass"} {
		for _, test := range []struct {
			name string
			note studioTakeNote
			want string
		}{
			{"below", studioTakeNote{Tick: 0, EndTick: 60, Note: 20, Velocity: 90}, "MIDI range 21–108"},
			{"above", studioTakeNote{Tick: 0, EndTick: 60, Note: 109, Velocity: 90}, "MIDI range 21–108"},
			{"expression", studioTakeNote{Tick: 0, EndTick: 60, Note: 60, Velocity: 90, NoteID: 1, Channel: 1, Expressions: []studioTakeExpression{{Tick: 0, PitchCents: 20, Pressure: .5, Timbre: .5}}}, "CICADA-UNSUPPORTED"},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				source := recordedKeysSource(kind)
				path := studioTestPath(t, string(source))
				handler, err := studioHandler(path)
				if err != nil {
					t.Fatal(err)
				}
				response := studioCall(t, handler, "/api/record", studioEdit{Revision: studioRevision(source), Track: "part", Pattern: "take", Take: []studioTakeNote{test.note}})
				got, err := os.ReadFile(path)
				if err != nil || response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), test.want) || !bytes.Equal(got, source) || len(studioHistoryEdits(t, handler)) != 0 {
					t.Fatalf("unsupported take changed source/history: %d %s %v", response.Code, response.Body.String(), err)
				}
			})
		}
	}
}
