package control

import (
	"encoding/json"
	"math"
	"testing"
)

func TestCaptureKeepsEveryGestureAndOrdersForcedRelease(t *testing.T) {
	n := Note{Tick: 100, EndTick: 100, Note: 60, Velocity: 100, NoteID: 7, Channel: 9}
	for _, point := range []NoteExpression{{Tick: 110, PitchCents: 25.125, Pressure: .2, Timbre: .5}, {Tick: 110, PitchCents: 50, Pressure: .8, Timbre: .75}, {Tick: 108, Pressure: 1, Timbre: .5}} {
		n.AddExpression(point)
	}
	n.End(NoteTime{Tick: 105})
	takes := []Recording{{Track: "voice", Pattern: "phrase", Notes: []Note{n}}}
	if err := ValidateRecordings(takes); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(takes)
	if err != nil {
		t.Fatal(err)
	}
	var restored []Recording
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	r := restored[0].Notes[0]
	if r.EndTick != 110 || len(r.Expressions) != 3 || r.Expressions[0].PitchCents != 25.125 || r.Expressions[1].Pressure != .8 || r.Expressions[2].Tick != 110 || r.NoteID != 7 || r.Channel != 9 {
		t.Fatal("capture changed the original snapshots", r)
	}
	for _, bad := range []NoteExpression{{Tick: 99, Timbre: .5}, {Tick: 111, Timbre: .5}, {Tick: 100, Pressure: math.NaN()}, {Tick: 100, PitchCents: 9601}, {Tick: 100, Timbre: 1.1}} {
		takes[0].Notes[0].Expressions = []NoteExpression{bad}
		if err := ValidateRecordings(takes); err == nil {
			t.Fatal("accepted bad gesture", bad)
		}
	}
}
