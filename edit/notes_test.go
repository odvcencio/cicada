package edit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func notesEdit(t testing.TB, source string, in *edit.EditNotes) (*edit.Result, error) {
	t.Helper()
	return edit.Apply([]byte(source), edit.Envelope{Version: 1, Revision: edit.Revision([]byte(source)), Intents: []edit.Intent{in}}, edit.Options{Compiler: gridCompiler{}})
}

func mustNotesEdit(t testing.TB, source string, in *edit.EditNotes) *edit.Result {
	t.Helper()
	result, err := notesEdit(t, source, in)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// expectNotesSource checks exact bytes for LF and CRLF sources.
func expectNotesSource(t *testing.T, before, after string, in *edit.EditNotes) {
	t.Helper()
	for _, newline := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll(before, "\n", newline)
		want := strings.ReplaceAll(after, "\n", newline)
		result := mustNotesEdit(t, source, in)
		if string(result.Source) != want {
			t.Fatalf("%s newline %q\n got:\n%s\nwant:\n%s", in.Op, newline, result.Source, want)
		}
		if result.Unchanged {
			t.Fatal("a real edit reported unchanged")
		}
	}
}

func expectNotesRefusal(t *testing.T, source string, in *edit.EditNotes, want string) {
	t.Helper()
	_, err := notesEdit(t, source, in)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: want refusal containing %q, got %v", in.Op, want, err)
	}
}

const notesPerformance = `cicada 2
instrument glass { voice poly { out = sine(pitch) * env(gate, 150ms) * (0.1 + pressure * 0.05 + timbre * 0.05) } }
track keys glass {}
pattern take performance length=3840ticks {
  // the take
  note 60 at=17ticks length=703ticks velocity=82;
  note 64 at=250ticks length=200ticks velocity=105;
  note 67 at=470ticks length=100ticks velocity=90 {
    gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
    gesture at=50ticks bend=50ct pressure=0.3 timbre=0.8;
  }
} // outside
scene main { keys=take }
song { main }
`

func performanceWith(notes ...string) string {
	return strings.Replace(notesPerformance, `  note 60 at=17ticks length=703ticks velocity=82;
  note 64 at=250ticks length=200ticks velocity=105;
  note 67 at=470ticks length=100ticks velocity=90 {
    gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
    gesture at=50ticks bend=50ct pressure=0.3 timbre=0.8;
  }
`, strings.Join(notes, ""), 1)
}

const gestureNote = `  note 67 at=480ticks length=100ticks velocity=90 {
    gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
    gesture at=50ticks bend=50ct pressure=0.3 timbre=0.8;
  }
`

func TestEditNotesQuantizesAPerformance(t *testing.T) {
	want := performanceWith("  note 60 at=0ticks length=703ticks velocity=82;\n", "  note 64 at=240ticks length=200ticks velocity=105;\n", gestureNote)
	expectNotesSource(t, notesPerformance, want, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240})
	// Strength moves each start part of the way: 17→9, 250→245, 470→475.
	half := performanceWith("  note 60 at=9ticks length=703ticks velocity=82;\n", "  note 64 at=245ticks length=200ticks velocity=105;\n", strings.Replace(gestureNote, "at=480ticks", "at=475ticks", 1))
	expectNotesSource(t, notesPerformance, half, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240, Strength: 50})
	// Named notes limit the quantize to the selection.
	one := performanceWith("  note 60 at=17ticks length=703ticks velocity=82;\n", "  note 64 at=240ticks length=200ticks velocity=105;\n", strings.Replace(gestureNote, "at=480ticks", "at=470ticks", 1))
	expectNotesSource(t, notesPerformance, one, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240, Notes: []edit.NoteRef{{At: 250, Pitch: 64, Length: 200, Velocity: 105}}})
	result := mustNotesEdit(t, notesPerformance, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240})
	if result.Label != "Notes · take · 3 notes quantized to 1/16" {
		t.Fatalf("label %q", result.Label)
	}
}

func TestEditNotesMovesResizesAndChangesPerformedNotes(t *testing.T) {
	ref := edit.NoteRef{At: 250, Pitch: 64, Length: 200, Velocity: 105}
	first := "  note 60 at=17ticks length=703ticks velocity=82;\n"
	gestures := strings.Replace(gestureNote, "at=480ticks", "at=470ticks", 1)
	expectNotesSource(t, notesPerformance, performanceWith(first, "  note 66 at=490ticks length=200ticks velocity=105;\n", gestures),
		&edit.EditNotes{Entity: "pattern:take", Op: "move", Notes: []edit.NoteRef{ref}, DT: 240, DP: 2})
	expectNotesSource(t, notesPerformance, performanceWith(first, "  note 64 at=250ticks length=200ticks velocity=105;\n", gestures, "  note 66 at=490ticks length=200ticks velocity=105;\n"),
		&edit.EditNotes{Entity: "pattern:take", Op: "move", Notes: []edit.NoteRef{ref}, DT: 240, DP: 2, Copy: true})
	expectNotesSource(t, notesPerformance, performanceWith(first, "  note 64 at=250ticks length=440ticks velocity=105;\n", gestures),
		&edit.EditNotes{Entity: "pattern:take", Op: "resize", Notes: []edit.NoteRef{ref}, DL: 240})
	expectNotesSource(t, notesPerformance, performanceWith(first, "  note 64 at=250ticks length=200ticks velocity=113;\n", gestures),
		&edit.EditNotes{Entity: "pattern:take", Op: "velocity", Notes: []edit.NoteRef{ref}, DV: 8})
	expectNotesSource(t, notesPerformance, performanceWith(first, "  note 64 at=250ticks length=200ticks velocity=40;\n", gestures),
		&edit.EditNotes{Entity: "pattern:take", Op: "velocity", Notes: []edit.NoteRef{ref}, Velocity: 40})
	expectNotesSource(t, notesPerformance, performanceWith(first, gestures),
		&edit.EditNotes{Entity: "pattern:take", Op: "delete", Notes: []edit.NoteRef{ref}})
	expectNotesSource(t, notesPerformance, performanceWith(first, "  note 64 at=250ticks length=200ticks velocity=105;\n", gestures, "  note 72 at=960ticks length=480ticks velocity=96;\n"),
		&edit.EditNotes{Entity: "pattern:take", Op: "insert", Insert: []edit.NoteRef{{At: 960, Pitch: 72, Length: 480, Velocity: 96}}})
}

func TestEditNotesRefusesWhatAPerformanceCannotHold(t *testing.T) {
	ref := edit.NoteRef{At: 250, Pitch: 64, Length: 200, Velocity: 105}
	gesture := edit.NoteRef{At: 470, Pitch: 67, Length: 100, Velocity: 90}
	all := []edit.NoteRef{{At: 17, Pitch: 60, Length: 703, Velocity: 82}, ref, gesture}
	for _, c := range []struct {
		name string
		in   *edit.EditNotes
		want string
	}{
		{"missing note", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 251, Pitch: 64, Length: 200, Velocity: 105}}, DT: 240}, "a selected note changed"},
		{"changed velocity", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 250, Pitch: 64, Length: 200, Velocity: 100}}, DT: 240}, "a selected note changed"},
		{"named twice", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref, ref}, DT: 240}, "a selected note changed"},
		{"before the start", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: -480}, "before the start of the loop"},
		{"past the loop end", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: 3500}, "past the loop end"},
		{"pitch range", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DP: 70}, "MIDI 0–127"},
		{"same-pitch overlap", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: -200, DP: -4}, "two C4 notes would overlap"},
		{"cut gesture", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{gesture}, DL: -60}, "shorter than its last point"},
		{"no note left", &edit.EditNotes{Op: "delete", Notes: all}, "keeps at least one note"},
		{"drum lane", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 250, Pitch: 64, Lane: "bd", Length: 200, Velocity: 105}}}, "no drum lanes"},
		{"inserted past the end", &edit.EditNotes{Op: "insert", Insert: []edit.NoteRef{{At: 3800, Pitch: 72, Length: 480, Velocity: 96}}}, "past the loop end"},
		{"unknown op", &edit.EditNotes{Op: "stretch"}, "choose a note operation"},
		{"shared policy", &edit.EditNotes{Op: "quantize", Grid: 240, Shared: "detach"}, "shared must be"},
		{"grid", &edit.EditNotes{Op: "quantize", Grid: 7}, "quantize grid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.in.Entity = "pattern:take"
			expectNotesRefusal(t, notesPerformance, c.in, c.want)
		})
	}
	// Two notes of one pitch rounding to the same grid line, and a start that
	// rounds later past the loop end.
	crowded := performanceWith("  note 60 at=130ticks length=200ticks velocity=82;\n", "  note 60 at=350ticks length=100ticks velocity=82;\n")
	expectNotesRefusal(t, crowded, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}, "two C4 notes would overlap at tick 240; a pitch plays one note at a time, so choose a finer grid")
	late := performanceWith("  note 60 at=3500ticks length=330ticks velocity=82;\n")
	expectNotesRefusal(t, late, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}, "past the loop end")
	var full []string
	for i := range 512 {
		full = append(full, fmt.Sprintf("  note %d at=%dticks length=5ticks velocity=90;\n", 36+i%64, i*7))
	}
	expectNotesRefusal(t, performanceWith(full...), &edit.EditNotes{Entity: "pattern:take", Op: "insert", Insert: []edit.NoteRef{{At: 3700, Pitch: 100, Length: 5, Velocity: 90}}}, "at most 512 notes")
}

func TestEditNotesPerformanceStartRoundingToTheLoopEndBeginsTheLoop(t *testing.T) {
	source := performanceWith("  note 60 at=3830ticks length=5ticks velocity=82;\n", "  note 64 at=500ticks length=200ticks velocity=105;\n")
	want := performanceWith("  note 60 at=0ticks length=5ticks velocity=82;\n", "  note 64 at=480ticks length=200ticks velocity=105;\n")
	expectNotesSource(t, source, want, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240})
}

func TestEditNotesUnchangedWritesNothing(t *testing.T) {
	onGrid := performanceWith("  note 60 at=0ticks length=703ticks velocity=82;\n")
	for _, c := range []struct {
		source  string
		in      *edit.EditNotes
		message string
	}{
		{onGrid, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}, "Already on the grid."},
		{onGrid, &edit.EditNotes{Entity: "pattern:take", Op: "velocity", Notes: []edit.NoteRef{{At: 0, Pitch: 60, Length: 703, Velocity: 82}}, Velocity: 82}, "Nothing changed."},
		{stepNotes, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 240}, "Already on the grid."},
		{stepNotes, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 120}, "Already on the grid."},
		{drumNotes, &edit.EditNotes{Entity: "pattern:beat", Op: "quantize", Grid: 240}, "Already on the grid."},
		{phraseNotes, &edit.EditNotes{Entity: "pattern:riff", Op: "move", Notes: []edit.NoteRef{{At: 480, Pitch: 64, Length: 240, Velocity: 100}}}, "Nothing changed."},
	} {
		result := mustNotesEdit(t, c.source, c.in)
		if !result.Unchanged || string(result.Source) != c.source || result.Response["message"] != c.message {
			t.Fatalf("%s %s: unchanged=%v message=%v\n%s", c.in.Entity, c.in.Op, result.Unchanged, result.Response["message"], result.Source)
		}
	}
}

const stepNotes = `cicada 2
instrument pad { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys pad {}
pattern harmony notes {
  . [c4 e4]^ - . // first beat
  g4 . a4 . . . . .
  velocity: 100 90 . . 70 . 64 . . . . .
}
scene verse { keys=harmony }
song { verse }
`

func TestEditNotesQuantizesAStepPattern(t *testing.T) {
	// The chord and its hold move to step 1, a4 to step 9, and every onset
	// keeps its velocity; the row changes only where a value moved.
	want := strings.NewReplacer(
		"  . [c4 e4]^ - . // first beat", "  [c4 e4]^ - . . // first beat",
		"  g4 . a4 . . . . .", "  g4 . . . a4 . . .",
		"velocity: 100 90", "velocity: 90 90",
	).Replace(stepNotes)
	expectNotesSource(t, stepNotes, want, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 960})
	result := mustNotesEdit(t, stepNotes, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 960})
	data := result.Plan.Patterns[0].Data
	if data[0] == nil || len(data[0].Notes) != 2 || !data[0].Accent || data[0].Velocity != 90 || !data[1].Tie || data[2] != nil || data[4].Note != 67 || data[4].Velocity != 70 || data[8].Note != 69 || data[8].Velocity != 64 || data[6] != nil {
		t.Fatalf("quantized steps lost notes or dynamics: %+v", data)
	}
	expectNotesRefusal(t, stepNotes, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 960, Strength: 50}, "quantize at 100% strength")
	expectNotesRefusal(t, stepNotes, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 400}, "whole number of 240-tick steps")
}

const stepMoves = `cicada 2
instrument pad { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys pad {}
pattern p notes {
  [c4 e4 g4]^?70 - . . c5 . . . // keep
  velocity: 80 . . . 100 . . .
}
scene verse { keys=p }
song { verse }
`

func stepRef(step int, pitch, length, velocity int) edit.NoteRef {
	return edit.NoteRef{At: int64(step) * 240, Pitch: pitch, Length: int64(length) * 240, Velocity: velocity}
}

func TestEditNotesEditsStepPatternChordsTiesAndVelocity(t *testing.T) {
	g4 := stepRef(0, 67, 2, 80)
	c5 := stepRef(4, 72, 1, 100)
	chord := []edit.NoteRef{stepRef(0, 60, 2, 80), stepRef(0, 64, 2, 80), g4}
	steps := func(from, to string) string {
		return strings.Replace(stepMoves, "  "+from+" // keep", "  "+to+" // keep", 1)
	}
	for _, c := range []struct {
		name string
		in   *edit.EditNotes
		want string
	}{
		// A chord splits: the pitch that moves carries accent, chance and velocity.
		{"split", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{g4}, DT: 480}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4]^?70 - g4^?70 - c5 . . .")},
		{"whole chord", &edit.EditNotes{Op: "move", Notes: chord, DT: 480}, steps("[c4 e4 g4]^?70 - . . c5 . . .", ". . [c4 e4 g4]^?70 - c5 . . .")},
		{"transpose chord", &edit.EditNotes{Op: "move", Notes: chord, DP: 12}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c5 e5 g5]^?70 - . . c5 . . .")},
		{"transpose one", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DP: -1}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4 g4]^?70 - . . b4 . . .")},
		{"copy", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: 480, Copy: true}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4 g4]^?70 - . . c5 . c5 .")},
		{"lengthen", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{g4}, DL: 240}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4 g4]^?70 - - . c5 . . .")},
		{"shorten", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{g4}, DL: -240}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4 g4]^?70 . . . c5 . . .")},
		{"delete a voice", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{g4}}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4]^?70 - . . c5 . . .")},
		{"delete the chord", &edit.EditNotes{Op: "delete", Notes: chord}, steps("[c4 e4 g4]^?70 - . . c5 . . .", ". . . . c5 . . .")},
		{"insert", &edit.EditNotes{Op: "insert", Insert: []edit.NoteRef{stepRef(6, 62, 2, 100)}}, steps("[c4 e4 g4]^?70 - . . c5 . . .", "[c4 e4 g4]^?70 - . . c5 . d4 -")},
		{"velocity", &edit.EditNotes{Op: "velocity", Notes: []edit.NoteRef{c5}, Velocity: 64}, strings.Replace(stepMoves, "velocity: 80 . . . 100", "velocity: 80 . . . 64", 1)},
		{"velocity of a chord voice", &edit.EditNotes{Op: "velocity", Notes: []edit.NoteRef{g4}, DV: 8}, strings.Replace(stepMoves, "velocity: 80 . . . 100", "velocity: 88 . . . 100", 1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.in.Entity = "pattern:p"
			expectNotesSource(t, stepMoves, c.want, c.in)
		})
	}
}

func TestEditNotesMergesAndCollapsesChords(t *testing.T) {
	source := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5 . . . // keep\n  velocity: 80 . . . 100 . . .", "[c4 e4]^ . g4 . c5 . . . // keep\n  velocity: 80 . 90 . 100 . . .", 1)
	g4 := stepRef(2, 67, 1, 90)
	// Same start and length: the moved note joins the chord, which keeps its accent and velocity.
	expectNotesSource(t, source, strings.Replace(source, "[c4 e4]^ . g4 .", "[c4 e4 g4]^ . . .", 1), &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{g4}, DT: -480})
	// Same start and pitch: the moved note (e4 up 3 semitones, g4) replaces the
	// stationary g4 and carries its chord's accent and velocity; one pitch is
	// left in the chord, which becomes a single note.
	e4 := stepRef(0, 64, 1, 80)
	replaced := strings.NewReplacer("[c4 e4]^ . g4 .", "c4^ . g4^ .", "velocity: 80 . 90", "velocity: 80 . 80").Replace(source)
	expectNotesSource(t, source, replaced, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{e4}, DT: 480, DP: 3})
	// Moving one voice onto another of its own chord is refused.
	expectNotesRefusal(t, source, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{stepRef(0, 60, 1, 80)}, DP: 4}, "two pitches of one chord would become equal")
}

func TestEditNotesRefusesWhatAStepGridCannotHold(t *testing.T) {
	g4 := stepRef(0, 67, 2, 80)
	c5 := stepRef(4, 72, 1, 100)
	four := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5", "[c4 e4 g4 b4]^?70 . . . c5", 1)
	slide := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5", "[c4 e4 g4]^?70 . . . c5~", 1)
	transposed := strings.Replace(stepMoves, "pattern p notes {", "pattern p notes transpose=24 {", 1)
	for _, c := range []struct {
		name, source string
		in           *edit.EditNotes
		want         string
	}{
		{"into a hold", stepMoves, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: -720}, "notes would overlap at step 2"},
		{"over a later onset", stepMoves, &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{g4}, DL: 720}, "notes would overlap at step 5"},
		{"past the end", stepMoves, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: 960}, "past the pattern start or end"},
		{"resize past the end", stepMoves, &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{c5}, DL: 960}, "past the pattern end"},
		{"different length", stepMoves, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: -960}, "share one length"},
		{"fifth pitch", strings.Replace(four, "c5", "d5", 1), &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{stepRef(4, 74, 1, 100)}, DT: -960}, "at most 4 pitches"},
		{"slide in a chord", slide, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{stepRef(4, 72, 1, 100)}, DT: -960}, "cannot slide or ratchet"},
		{"pitch range", stepMoves, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DP: 56}, "MIDI 0–127"},
		{"pitch range with transpose", transposed, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DP: 40}, "counting the pattern transpose"},
		{"part of a step", stepMoves, &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: 100}, "whole steps of 240 ticks"},
		{"tie alone", stepMoves, &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{stepRef(1, 67, 1, 80)}}, "a selected note changed"},
		{"stale length", stepMoves, &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{stepRef(0, 67, 1, 80)}}, "a selected note changed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.in.Entity = "pattern:p"
			expectNotesRefusal(t, c.source, c.in, c.want)
		})
	}
}

func TestEditNotesAddsAVelocityRowWhenNeeded(t *testing.T) {
	source := strings.Replace(stepMoves, "  velocity: 80 . . . 100 . . .\n", "", 1)
	want := strings.Replace(stepMoves, "  velocity: 80 . . . 100 . . .\n", "  velocity: 100 . . . 64 . . .\n", 1)
	expectNotesSource(t, source, want, &edit.EditNotes{Entity: "pattern:p", Op: "velocity", Notes: []edit.NoteRef{stepRef(4, 72, 1, 100)}, Velocity: 64})
	// Inline patterns get an inline row; an expression row keeps its place after it.
	inline := "track bass acid {}\npattern p acid { c4 . e4 . }\nscene main { bass=p }\nsong { main }\n"
	expectNotesSource(t, inline, strings.Replace(inline, "{ c4 . e4 . }", "{ c4 . e4 . velocity: 100 . 30 . }", 1),
		&edit.EditNotes{Entity: "pattern:p", Op: "velocity", Notes: []edit.NoteRef{stepRef(2, 64, 1, 100)}, Velocity: 30})
	expression := "cicada 2\ntrack bass acid {}\npattern p acid {\n  c4 . e4 .\n  bend: 0ct 0ct 100ct 0ct\n}\nscene main { bass=p }\nsong { main }\n"
	expectNotesSource(t, expression, strings.Replace(expression, "  bend:", "  velocity: 100 . 30 .\n  bend:", 1),
		&edit.EditNotes{Entity: "pattern:p", Op: "velocity", Notes: []edit.NoteRef{stepRef(2, 64, 1, 100)}, Velocity: 30})
}

const phraseNotes = `track bass acid {}
phrase hook { c4 . e4 . }
pattern riff acid { use hook use hook }
pattern other acid { use hook }
scene main { bass=riff }
song { main }
`

func TestEditNotesExpandsPhraseUsesInsideThePattern(t *testing.T) {
	want := strings.Replace(phraseNotes, "pattern riff acid { use hook use hook }", "pattern riff acid { c4 . . e4 c4 . e4 . }", 1)
	expectNotesSource(t, phraseNotes, want, &edit.EditNotes{Entity: "pattern:riff", Op: "move", Notes: []edit.NoteRef{stepRef(2, 64, 1, 100)}, DT: 240})
}

func TestEditNotesRefusesAcidChordsAndTupletGroups(t *testing.T) {
	acid := "track bass acid {}\npattern p acid { c4 . e4 . }\nscene main { bass=p }\nsong { main }\n"
	expectNotesRefusal(t, acid, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{stepRef(2, 64, 1, 100)}, DT: -480}, "an acid pattern plays one pitch per step")
	tuplet := "track bass acid {}\npattern p acid { [c4 e4 g4] . e4 . }\nscene main { bass=p }\nsong { main }\n"
	expectNotesRefusal(t, tuplet, &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960}, "tuplet groups")
}

const drumNotes = `track drums drums {}
pattern beat drums {
  bd: x . . x . . . . x . . . . . . .
  sd: . . . . X . . . . . . . . x9*2 . .
}
scene main { drums=beat }
song { main }
`

func TestEditNotesEditsDrumLanes(t *testing.T) {
	expectNotesSource(t, drumNotes, strings.NewReplacer(
		"bd: x . . x . . . . x", "bd: x . . . x . . . x",
		"sd: . . . . X . . . . . . . . x9*2 . .", "sd: . . . . X . . . . . . . x9*2 . . .",
	).Replace(drumNotes), &edit.EditNotes{Entity: "pattern:beat", Op: "quantize", Grid: 960})
	hit := edit.NoteRef{At: 13 * 240, Lane: "sd", Length: 240, Velocity: 126}
	expectNotesSource(t, drumNotes, strings.Replace(drumNotes, ". x9*2 . .", ". X*2 . .", 1), &edit.EditNotes{Entity: "pattern:beat", Op: "velocity", Notes: []edit.NoteRef{hit}, DV: 8})
	expectNotesSource(t, drumNotes, strings.Replace(drumNotes, ". x9*2 . .", ". x4*2 . .", 1), &edit.EditNotes{Entity: "pattern:beat", Op: "velocity", Notes: []edit.NoteRef{hit}, Velocity: 56})
	// A lane move copies the hit with its ratchet; a hit in the target cell is replaced.
	expectNotesSource(t, drumNotes, strings.NewReplacer("bd: x . . x . . . . x . . . . . . .", "bd: x . . x . . . . x . . . . x9*2 . .", ". . . . . x9*2 . .", ". . . . . . . .").Replace(drumNotes),
		&edit.EditNotes{Entity: "pattern:beat", Op: "move", Notes: []edit.NoteRef{hit}, DP: -1})
	expectNotesSource(t, drumNotes, strings.Replace(drumNotes, "bd: x . . x", "bd: . . . x", 1), &edit.EditNotes{Entity: "pattern:beat", Op: "delete", Notes: []edit.NoteRef{{At: 0, Lane: "bd", Length: 240, Velocity: 100}}})
	expectNotesSource(t, drumNotes, strings.Replace(drumNotes, "bd: x . . x", "bd: x X . x", 1), &edit.EditNotes{Entity: "pattern:beat", Op: "insert", Insert: []edit.NoteRef{{At: 240, Lane: "bd", Length: 240, Velocity: 127}}})
	for _, c := range []struct {
		in   *edit.EditNotes
		want string
	}{
		{&edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{hit}, DL: 240}, "drum hits have a fixed length"},
		{&edit.EditNotes{Op: "move", Notes: []edit.NoteRef{hit}, DP: 1}, "past the first or last lane"},
		{&edit.EditNotes{Op: "move", Notes: []edit.NoteRef{hit}, DT: 960}, "past the pattern start or end"},
		{&edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 0, Lane: "bd", Length: 240, Velocity: 100}, {At: 720, Lane: "bd", Length: 240, Velocity: 100}}, DT: 0, DP: 0}, ""},
		{&edit.EditNotes{Op: "quantize", Grid: 3840}, "two selected hits would land on bd step 1"},
		{&edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 240, Lane: "bd", Length: 240, Velocity: 100}}}, "a selected note changed"},
		{&edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 0, Lane: "cp", Length: 240, Velocity: 100}}}, "a selected note changed"},
	} {
		c.in.Entity = "pattern:beat"
		if c.want == "" {
			if result := mustNotesEdit(t, drumNotes, c.in); !result.Unchanged {
				t.Fatal("a move by zero changed the pattern")
			}
			continue
		}
		expectNotesRefusal(t, drumNotes, c.in, c.want)
	}
}

// Rule 2 for EditNotes: a batch changes only the tokens it must. Comments,
// spacing and every other declaration keep their bytes and offsets.
func TestFidelityRule2_EditNotesTouchesOnlyItsTokens(t *testing.T) {
	result := mustNotesEdit(t, stepMoves, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{stepRef(4, 72, 1, 100)}, DT: 480})
	before, after := []byte(stepMoves), result.Source
	at := bytes.Index(before, []byte("c5 . . . // keep"))
	window := len("c5 . . .")
	if len(after) != len(before) || !bytes.Equal(after[:at], before[:at]) || !bytes.Equal(after[at+window:], before[at+window:]) || string(after[at:at+window]) != ". . c5 ." {
		t.Fatalf("a one-note move changed bytes outside its two step tokens:\n%s", after)
	}
	// Selected notes that land together are refused, never silently replaced.
	repeated := strings.Replace(stepMoves, "c5 . . . // keep", "c5 c5 . . // keep", 1)
	expectNotesRefusal(t, repeated, &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960}, "two selected notes would land on the same pitch")
}

func TestEditNotesOneBatchOfSixteenNotes(t *testing.T) {
	var notes []string
	var refs []edit.NoteRef
	for i := range 16 {
		at := int64(i*240 + 11 + i%3)
		notes = append(notes, fmt.Sprintf("  note %d at=%dticks length=120ticks velocity=%d;\n", 48+i, at, 70+i))
		refs = append(refs, edit.NoteRef{At: at, Pitch: 48 + i, Length: 120, Velocity: 70 + i})
	}
	source := performanceWith(notes...)
	result := mustNotesEdit(t, source, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240, Notes: refs})
	performance := result.Plan.Patterns[0].Performance
	if len(performance.Notes) != 16 || result.Label != "Notes · take · 16 notes quantized to 1/16" {
		t.Fatalf("batch: %d notes, label %q", len(performance.Notes), result.Label)
	}
	for i, note := range performance.Notes {
		if note.Tick != int64(i*240) || note.EndTick != note.Tick+120 || int(note.Note) != 48+i || int(note.Velocity) != 70+i {
			t.Fatalf("note %d: %+v", i, note)
		}
	}
	// The intent round-trips through the public envelope as one intent.
	raw, err := json.Marshal(map[string]any{"version": 1, "intents": []any{map[string]any{"kind": "editnotes", "entity": "pattern:take", "op": "quantize", "grid": 240, "notes": refs}}})
	if err != nil {
		t.Fatal(err)
	}
	var env edit.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Intents) != 1 || len(env.Intents[0].(*edit.EditNotes).Notes) != 16 {
		t.Fatalf("envelope: %v %+v", err, env)
	}
	env.Revision = edit.Revision([]byte(source))
	decoded, err := edit.Apply([]byte(source), env, edit.Options{Compiler: gridCompiler{}})
	if err != nil || !bytes.Equal(decoded.Source, result.Source) {
		t.Fatalf("decoded batch differs: %v", err)
	}
}

func TestEditNotesEditsAPerformanceInItsOwningFile(t *testing.T) {
	part := "track keys piano {}\npattern played performance length=3840ticks { note 60 at=17ticks length=703ticks velocity=82; }\nscene main { keys=played }\nsong { main }\n"
	main := "cicada 2\n// entry\n"
	files := []notation.SourceFile{{Path: "main.cicada", Source: []byte(main)}, {Path: "part.cicada", Source: []byte(part)}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	result, err := edit.Apply([]byte(main), edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.EditNotes{Entity: "pattern:played", Op: "quantize", Grid: 240}}}, edit.Options{Compiler: compiler, Path: "main.cicada", Edition: 2, Sources: files})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Source) != main || len(result.Files) != 1 || result.Files[0].Path != "part.cicada" || !bytes.Contains(result.Files[0].After, []byte("note 60 at=0ticks length=703ticks velocity=82;")) {
		t.Fatalf("performance edit changed the wrong file: %+v", result.Files)
	}
}

// BenchmarkEditNotesQuantize500 times one 500-note quantize, from source to
// the validated candidate: two compiles, the edit and one rewrite.
func BenchmarkEditNotesQuantize500(b *testing.B) {
	var notes []string
	for i := range 500 {
		notes = append(notes, fmt.Sprintf("  note %d at=%dticks length=5ticks velocity=90;\n", 36+i%60, i*7+3))
	}
	source := performanceWith(notes...)
	in := &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}
	if result := mustNotesEdit(b, source, in); result.Unchanged {
		b.Fatal("benchmark fixture is already on the grid")
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := notesEdit(b, source, in); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEditNotesQuantize64Steps times a quantize of a full 64-step pattern.
func BenchmarkEditNotesQuantize64Steps(b *testing.B) {
	tokens := make([]string, 64)
	for i := range tokens {
		tokens[i] = "."
		if i%4 == 1 {
			tokens[i] = sourceNoteName(48 + i%24)
		}
	}
	source := "track bass acid {}\npattern p acid { " + strings.Join(tokens, " ") + " }\nscene main { bass=p }\nsong { main }\n"
	in := &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960}
	if result := mustNotesEdit(b, source, in); result.Unchanged {
		b.Fatal("benchmark fixture is already on the grid")
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := notesEdit(b, source, in); err != nil {
			b.Fatal(err)
		}
	}
}

func sourceNoteName(pitch int) string {
	return [...]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}[pitch%12] + fmt.Sprint(pitch/12-1)
}
