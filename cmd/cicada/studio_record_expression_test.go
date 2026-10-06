package main

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func recordedPattern(t *testing.T, source []byte, id string) project.Pattern {
	t.Helper()
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		t.Fatalf("recorded source parse: %+v\n%s", diagnostics, source)
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil || hasDiagnosticErrors(diagnostics) {
		t.Fatalf("recorded source compile: %+v\n%s", diagnostics, source)
	}
	for _, pattern := range semantic.Patterns {
		if pattern.ID == id {
			return pattern
		}
	}
	t.Fatalf("recorded pattern %q missing", id)
	return project.Pattern{}
}

func TestRecordedExpressionTakeWritesRowsOnHeldTies(t *testing.T) {
	source, err := recordedTakeSource([]byte(studioScore), "bass", "pulse", []studioTakeNote{{
		Tick: 0, EndTick: 3 * seq.TicksPerStep, Note: 60, Velocity: 96, NoteID: 12, Channel: 1,
		Expressions: []studioTakeExpression{
			{Tick: 0, PitchCents: 50, Pressure: .25, Timbre: .75},
			{Tick: seq.TicksPerStep, PitchCents: 100, Pressure: .5, Timbre: .25},
			{Tick: 2 * seq.TicksPerStep, PitchCents: -50, Pressure: 1, Timbre: 1},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pattern := recordedPattern(t, source, "pulse")
	if pattern.Data[0].Note != 60 || !pattern.Data[1].Tie || !pattern.Data[2].Tie || pattern.Data[3] != nil {
		t.Fatalf("held take did not sustain through tied steps: %+v", pattern.Data)
	}
	want := []project.NoteExpression{
		{PitchCents: 50, Pressure: .25, Timbre: .75},
		{PitchCents: 100, Pressure: .5, Timbre: .25},
		{PitchCents: -50, Pressure: 1, Timbre: 1},
		{Timbre: .5},
	}
	for i, value := range want {
		if pattern.Expression[i] != value {
			t.Fatalf("expression step %d=%+v, want %+v\n%s", i, pattern.Expression[i], value, source)
		}
	}
	for _, row := range []string{"bend:", "vibrato:", "pressure:", "timbre:"} {
		if !strings.Contains(string(source), row) {
			t.Fatalf("take omitted %s", row)
		}
	}
}

func TestRecordedExpressionTakeSeparatesVibratoCenterAndDepth(t *testing.T) {
	source, err := recordedTakeSource([]byte(studioScore), "bass", "pulse", []studioTakeNote{{
		Tick: 0, EndTick: 120, Note: 60, Velocity: 96,
		Expressions: []studioTakeExpression{
			{Tick: 0, PitchCents: 30, Timbre: .5},
			{Tick: 10, PitchCents: 70, Timbre: .5},
			{Tick: 20, PitchCents: 30, Timbre: .5},
			{Tick: 30, PitchCents: 70, Timbre: .5},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	value := recordedPattern(t, source, "pulse").Expression[0]
	if value.PitchCents != 50 || value.VibratoDepthCents != 20 {
		t.Fatalf("oscillation lost center or depth: %+v\n%s", value, source)
	}
	if _, _, ok := takeVibrato([]studioTakeExpression{{PitchCents: -20}, {PitchCents: 0}, {PitchCents: 10}, {PitchCents: 20}}); ok {
		t.Fatal("a one-way bend was classified as vibrato")
	}
}

func TestRecordedExpressionTakeUpdatesRowsWithoutDuplicatingOrLosingComments(t *testing.T) {
	source := strings.Replace(studioScore, "{ 1 . 5 . }", "{ 1 . 5 .\n  bend: 10ct . . . // keep bend comment\n  vibrato: 4ct . . .\n  pressure: 0 . . .\n  timbre: 0.5 . . .\n  // } keep closing-brace comment\n}", 1)
	updated, err := recordedTakeSource([]byte(source), "bass", "pulse", []studioTakeNote{{
		Tick: 0, EndTick: 120, Note: 60, Velocity: 96, Expressions: []studioTakeExpression{{Tick: 0, PitchCents: 50, Timbre: .75}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pattern := recordedPattern(t, updated, "pulse")
	if pattern.Expression[0].PitchCents != 50 || pattern.Expression[0].VibratoDepthCents != 0 || pattern.Expression[1].PitchCents != 0 {
		t.Fatalf("existing expression was not updated and reset: %+v", pattern.Expression)
	}
	for _, row := range []string{"bend:", "vibrato:", "pressure:", "timbre:"} {
		if strings.Count(string(updated), row) != 1 {
			t.Fatalf("duplicated expression row %q\n%s", row, updated)
		}
	}
	if !strings.Contains(string(updated), "// keep bend comment") || !strings.Contains(string(updated), "// } keep closing-brace comment") {
		t.Fatalf("recording discarded a comment\n%s", updated)
	}
}

func TestRecordedExpressionTakeSupportsProgrammableNoteTracks(t *testing.T) {
	source := []byte(`cicada 1
instrument voice { voice mono { out = saw(pitch); } }
track lead voice {}
pattern line notes steps=4 { c4 . . . }
scene main { lead=line }
song { main }
`)
	depth := float64(12)
	updated, err := recordedTakeSource(source, "lead", "line", []studioTakeNote{{
		Tick: 0, EndTick: 2 * seq.TicksPerStep, Note: 64, Velocity: 90, NoteID: 1, Channel: 14,
		Expressions: []studioTakeExpression{{Tick: 0, PitchCents: 25, Pressure: .8, Timbre: .2, VibratoDepthCents: &depth}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pattern := recordedPattern(t, updated, "line")
	if pattern.Data[0].Note != 64 || !pattern.Data[1].Tie || pattern.Expression[0].VibratoDepthCents != 12 {
		t.Fatalf("programmable voice capture was not preserved: %+v", pattern)
	}
}

func TestRecordedExpressionTakeRejectsAmbiguousPolyphonyAndInvalidSamples(t *testing.T) {
	base := studioTakeNote{Tick: 0, EndTick: 240, Note: 60, Velocity: 96, Expressions: []studioTakeExpression{{Tick: 0, Timbre: .5}}}
	for _, take := range [][]studioTakeNote{
		{base, {Tick: 120, EndTick: 360, Note: 64, Velocity: 96}},
		{{Tick: 0, EndTick: 10, Note: 60, Velocity: 96, Expressions: base.Expressions}, {Tick: 20, EndTick: 40, Note: 64, Velocity: 96}},
		{{Tick: 0, EndTick: 5 * seq.TicksPerStep, Note: 60, Velocity: 96, Expressions: base.Expressions}},
	} {
		if _, err := recordedTakeSource([]byte(studioScore), "bass", "pulse", take); err == nil {
			t.Fatalf("accepted ambiguous expressive take: %+v", take)
		}
	}
	for _, expression := range []studioTakeExpression{
		{Tick: -1, Timbre: .5}, {Tick: 241, Timbre: .5}, {PitchCents: 9601}, {Pressure: 1.01}, {Timbre: -.01}, {PitchCents: math.NaN()},
	} {
		note := base
		note.Expressions = []studioTakeExpression{expression}
		if _, err := recordedTakeSource([]byte(studioScore), "bass", "pulse", []studioTakeNote{note}); err == nil {
			t.Fatalf("accepted invalid expression: %+v", expression)
		}
	}
}

func TestRecordedExpressionTakeRefusesExistingChords(t *testing.T) {
	_, err := recordedTakeSource([]byte(studioChordScore), "keys", "harmony", []studioTakeNote{{
		Tick: 0, EndTick: seq.TicksPerStep, Note: 62, Velocity: 96,
		Expressions: []studioTakeExpression{{Tick: 0, PitchCents: 20, Pressure: .5, Timbre: .5}},
	}})
	if err == nil || !strings.Contains(err.Error(), "without chord steps") {
		t.Fatalf("expressive recording could overwrite a chord: %v", err)
	}
}
