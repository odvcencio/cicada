package edit_test

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

func TestRecordedTakeRefusesNotesThatShareADestination(t *testing.T) {
	for _, tc := range []struct {
		name, track, pattern string
		notes                []edit.TakeNote
	}{
		{"substep chord", "bass", "pulse", []edit.TakeNote{{Note: 60, Velocity: 90, EndTick: 10}, {Tick: 20, EndTick: 30, Note: 64, Velocity: 100}}},
		{"repeated phrase", "bass", "pulse", []edit.TakeNote{{Note: 60, Velocity: 90, EndTick: 60}, {Tick: 960, EndTick: 1020, Note: 60, Velocity: 90}}},
		{"drum aliases", "drums", "beat", []edit.TakeNote{{Note: 41, Velocity: 90}, {Note: 43, Velocity: 100}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := recordTakeSource(t, []byte(recordScore), tc.track, tc.pattern, tc.notes)
			if err == nil || !strings.Contains(err.Error(), "multiple notes") {
				t.Fatalf("ambiguous take was not refused: %v", err)
			}
		})
	}
	in := &edit.RecordTake{Recordings: []edit.Recording{
		{Track: "bass", Pattern: "pulse", Notes: []edit.TakeNote{{Note: 60, Velocity: 90}}},
		{Track: "bass", Pattern: "pulse", Notes: []edit.TakeNote{{Tick: 240, EndTick: 240, Note: 64, Velocity: 90}}},
	}}
	if _, err := applyM5(t, []byte(recordScore), in); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate pattern accepted: %v", err)
	}
}

func TestRecordedTakeUsesAuthoredStepSize(t *testing.T) {
	for _, grid := range []struct {
		value string
		ticks int64
	}{{"1/8", 480}, {"1/32", 120}} {
		for _, expressive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expression=%v", grid.value, expressive), func(t *testing.T) {
				source := []byte(fmt.Sprintf("cicada 2\ntrack part acid {}\npattern take acid steps=8 step=%s { . . . . . . . . }\nscene main { part=take }\nsong { main }\n", grid.value))
				notes := []edit.TakeNote{{Tick: grid.ticks, EndTick: 3 * grid.ticks, Note: 64, Velocity: 100}}
				if expressive {
					notes[0].Expressions = []edit.TakeExpression{{Tick: grid.ticks, Pressure: .5, Timbre: .5}}
				}
				updated, err := recordTakeSource(t, source, "part", "take", notes)
				if err != nil {
					t.Fatal(err)
				}
				pattern := recordedPattern(t, updated, "take")
				if pattern.Data[0] != nil || pattern.Data[1] == nil || pattern.Data[1].Note != 64 {
					t.Fatalf("note missed authored grid: %+v\n%s", pattern.Data, updated)
				}
				if expressive && (pattern.Data[2] == nil || !pattern.Data[2].Tie || pattern.Expression[1].Pressure != .5) {
					t.Fatalf("expression duration missed authored grid: %+v\n%s", pattern.Data, updated)
				}
			})
		}
	}
}
