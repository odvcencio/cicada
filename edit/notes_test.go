package edit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
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

// expectNoteError checks a refusal that names its note: the message, the
// reason code and the note's identity as structured fields.
func expectNoteError(t *testing.T, source string, in *edit.EditNotes, code, want string, note map[string]any) {
	t.Helper()
	_, err := notesEdit(t, source, in)
	var refusal *edit.NoteError
	if !errors.As(err, &refusal) {
		t.Fatalf("%s: want a NoteError %q, got %v", in.Op, code, err)
	}
	if refusal.Code != code || !strings.Contains(refusal.Error(), want) {
		t.Fatalf("%s: got code %q, message %q; want %q containing %q", in.Op, refusal.Code, refusal.Error(), code, want)
	}
	if !slices.Contains(edit.NoteErrorCodes, code) {
		t.Fatalf("code %q is not listed in NoteErrorCodes", code)
	}
	if note != nil && !reflect.DeepEqual(refusal.Note(), note) {
		t.Fatalf("%s: note %v, want %v", in.Op, refusal.Note(), note)
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
		name, code, want string
		in               *edit.EditNotes
	}{
		{"missing note", "note-changed", "note 64 (E4) at tick 251: a selected note changed (no note of this pitch starts at this tick); select the notes again", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 251, Pitch: 64, Length: 200, Velocity: 105}}, DT: 240}},
		{"changed velocity", "note-changed", "note 64 (E4) at tick 250: a selected note changed (its velocity is 105, not 100)", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 250, Pitch: 64, Length: 200, Velocity: 100}}, DT: 240}},
		{"changed length", "note-changed", "(it lasts 200 ticks, not 210)", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{{At: 250, Pitch: 64, Length: 210, Velocity: 105}}, DT: 240}},
		{"named twice", "note-changed", "(the batch names it twice)", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref, ref}, DT: 240}},
		{"before the start", "outside-loop", "note 64 (E4) at tick 250: move would start it at tick -230, before the loop starts", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: -480}},
		{"past the loop end", "outside-loop", "note 64 (E4) at tick 250: move would end it at tick 3950, past the loop end at tick 3840", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: 3500}},
		{"pitch range", "pitch-range", "note 64 (E4) at tick 250: move would make it MIDI 134, which with the pattern transpose of +0 leaves 0–127", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DP: 70}},
		{"same-pitch overlap", "overlap", "note 64 (E4) at tick 250: move would put it at tick 50 to 250 as C4, over the C4 at tick 17 to 720; a pitch plays one note at a time, so move one of them first", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: -200, DP: -4}},
		{"copy overlap", "overlap", "note 64 (E4) at tick 250: copy would put it at tick 300 to 500 as E4, over the E4 at tick 250 to 450", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{ref}, DT: 50, Copy: true}},
		{"too short", "too-short", "note 64 (E4) at tick 250: resize would leave it 0 ticks long", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{ref}, DL: -200}},
		{"cut gesture", "gesture-cut", "note 67 (G4) at tick 470: resize to 40 ticks would cut its gesture point at 50 ticks; a note cannot become shorter than its last gesture point", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{gesture}, DL: -60}},
		{"drum lane", "no-lanes", `note 64 (E4) at tick 250: a played performance has no drum lanes, so lane "bd" cannot be named`, &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 250, Pitch: 64, Lane: "bd", Length: 200, Velocity: 105}}}},
		{"inserted past the end", "outside-loop", "note 72 (C5) at tick 3800: insert would end it at tick 4280, past the loop end at tick 3840", &edit.EditNotes{Op: "insert", Insert: []edit.NoteRef{{At: 3800, Pitch: 72, Length: 480, Velocity: 96}}}},
		{"invalid note", "invalid-note", "notes[0] (at 250, pitch 64, length 200, velocity 128)", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 250, Pitch: 64, Length: 200, Velocity: 128}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.in.Entity = "pattern:take"
			expectNoteError(t, notesPerformance, c.in, c.code, c.want, nil)
		})
	}
	for _, c := range []struct {
		name string
		in   *edit.EditNotes
		want string
	}{
		{"no note left", &edit.EditNotes{Op: "delete", Notes: all}, "keeps at least one note"},
		{"unknown op", &edit.EditNotes{Op: "stretch"}, "choose a note operation"},
		{"shared policy", &edit.EditNotes{Op: "quantize", Grid: 240, Shared: "detach"}, "shared must be"},
		{"grid", &edit.EditNotes{Op: "quantize", Grid: 7}, "quantize grid"},
		{"velocity and dv", &edit.EditNotes{Op: "velocity", Notes: []edit.NoteRef{ref}, Velocity: 90, DV: 8}, "set velocity (a new value) or dv (a change), not both"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.in.Entity = "pattern:take"
			expectNotesRefusal(t, notesPerformance, c.in, c.want)
		})
	}
	// Two notes of one pitch rounding to the same grid line, and a start that
	// rounds later past the loop end.
	crowded := performanceWith("  note 60 at=130ticks length=200ticks velocity=82;\n", "  note 60 at=350ticks length=100ticks velocity=82;\n")
	expectNoteError(t, crowded, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}, "overlap", "note 60 (C4) at tick 350: quantize would put it at tick 240 to 340 as C4, over the C4 at tick 240 to 440; a pitch plays one note at a time, so choose a finer grid", map[string]any{"at": int64(350), "pitch": 60})
	late := performanceWith("  note 60 at=3500ticks length=330ticks velocity=82;\n")
	expectNoteError(t, late, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}, "outside-loop", "note 60 (C4) at tick 3500: quantize would move it to tick 3600 and end it at tick 3930, past the loop end at tick 3840", nil)
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
	source := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5 . . . // keep", "[c4 e4]^ . g4^ . c5 . . . // keep", 1)
	g4 := stepRef(2, 67, 1, 80)
	// Same start, length, accent, chance and velocity: the moved note joins the chord.
	expectNotesSource(t, source, strings.Replace(source, "[c4 e4]^ . g4^ .", "[c4 e4 g4]^ . . .", 1), &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{g4}, DT: -480})
	// Same start and pitch: the moved note (e4 up 3 semitones, g4) replaces the
	// unselected g4 and carries its chord's accent and velocity; one pitch is
	// left in the chord, which becomes a single note.
	e4 := stepRef(0, 64, 1, 80)
	replaced := strings.Replace(source, "[c4 e4]^ . g4^ .", "c4^ . g4^ .", 1)
	expectNotesSource(t, source, replaced, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{e4}, DT: 480, DP: 3})
	// Moving one voice onto another of its own chord is refused.
	expectNoteError(t, source, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{stepRef(0, 60, 1, 80)}, DP: 4}, "chord-pitches-equal", "step 1, c4: move would make it e4, equal to another pitch of its chord on step 1", nil)
}

func TestEditNotesRefusesWhatAStepGridCannotHold(t *testing.T) {
	g4 := stepRef(0, 67, 2, 80)
	c5 := stepRef(4, 72, 1, 100)
	four := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5", "[c4 e4 g4 b4]^?70 . . . c5", 1)
	slide := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5", "[c4 e4 g4]^?70 . . . c5~", 1)
	transposed := strings.Replace(stepMoves, "pattern p notes {", "pattern p notes transpose=24 {", 1)
	for _, c := range []struct {
		name, source, code, want string
		in                       *edit.EditNotes
	}{
		{"into a hold", stepMoves, "overlap", "step 5, c5: move would put it on step 2, inside the c4 and e4 and g4 on step 1, which holds through step 2; a new note ends the one before it", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: -720}},
		{"over a later onset", stepMoves, "overlap", "step 1, c4: resize would hold it through step 5, over the c5 on step 5", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{g4}, DL: 720}},
		{"past the end", stepMoves, "outside-pattern", "step 5, c5: move would put it on step 9, 1 step long, past the pattern end (the pattern has 8 steps)", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: 960}},
		{"resize past the end", stepMoves, "outside-pattern", "step 5, c5: resize to 5 steps would hold it past the pattern end at step 8", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{c5}, DL: 960}},
		{"too short", stepMoves, "too-short", "step 1, c4: resize would leave it 0 steps long", &edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{g4}, DL: -480}},
		{"different length", stepMoves, "chord-length", "step 5, c5: move would start it, 1 step long, on step 1 with c4 and e4 and g4, 2 steps long; notes that start together share one length", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DT: -960}},
		{"fifth pitch", strings.Replace(four, "c5", "d5", 1), "chord-size", "step 5, d5: move would add it to the chord of c4 and e4 and g4 and b4 on step 1, making 5 pitches; a chord holds at most 4", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{stepRef(4, 74, 1, 100)}, DT: -960}},
		{"slide in a chord", slide, "chord-articulation", "step 5, c5: move would make a chord of c5 and c4 and e4 and g4 on step 1, and one of them slides or ratchets", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{stepRef(4, 72, 1, 100)}, DT: -960}},
		{"pitch range", stepMoves, "pitch-range", "step 5, c5: move would make it MIDI 128, which with the pattern transpose of +0 leaves 0–127", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DP: 56}},
		{"pitch range with transpose", transposed, "pitch-range", "step 5, c5: move would make it MIDI 112, which with the pattern transpose of +24 leaves 0–127", &edit.EditNotes{Op: "move", Notes: []edit.NoteRef{c5}, DP: 40}},
		{"tie alone", stepMoves, "note-changed", "the g4 at tick 240: a selected note changed (that step holds the note from step 1; a hold cannot be selected alone)", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{stepRef(1, 67, 1, 80)}}},
		{"stale length", stepMoves, "note-changed", "the g4 at tick 0: a selected note changed (it lasts 480 ticks, not 240)", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{stepRef(0, 67, 1, 80)}}},
		{"off the step grid", stepMoves, "note-changed", "the c5 at tick 970: a selected note changed (it does not start on a 240-tick step)", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 970, Pitch: 72, Length: 240, Velocity: 100}}}},
		{"lane in a notes pattern", stepMoves, "no-lanes", "a notes pattern has no drum lanes", &edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 960, Pitch: 72, Lane: "bd", Length: 240, Velocity: 100}}}},
		{"insert off the grid", stepMoves, "not-whole-steps", "the d4 at tick 1500: insert needs a start and a length of whole 240-tick steps", &edit.EditNotes{Op: "insert", Insert: []edit.NoteRef{{At: 1500, Pitch: 62, Length: 240, Velocity: 100}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.in.Entity = "pattern:p"
			expectNoteError(t, c.source, c.in, c.code, c.want, nil)
		})
	}
	expectNoteError(t, stepMoves, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{c5}, DT: 960}, "outside-pattern", "step 5, c5", map[string]any{"at": int64(960), "step": 5, "pitch": 72})
	expectNotesRefusal(t, stepMoves, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{c5}, DT: 100}, "a move of 100 ticks is not a whole number of 240-tick steps")
}

const mixedDynamics = `cicada 2
instrument pad { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys pad {}
pattern p notes {
  c4 e4?50 . .
  velocity: 100 60 . .
}
scene verse { keys=p }
song { verse }
`

// A merge keeps one accent, chance and velocity for the chord. Quantize
// refuses to drop the moved note's own and names what it would lose; notes
// whose dynamics match still merge.
func TestEditNotesRefusesAMergeThatChangesDynamics(t *testing.T) {
	expectNoteError(t, mixedDynamics, &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960}, "chord-modifiers",
		"step 2, e4: after quantize, it would join c4 in a chord on step 1 and lose its 50% chance and play at velocity 100 instead of 60; notes that start together share accent, chance and velocity, so make them match first",
		map[string]any{"at": int64(240), "step": 2, "pitch": 64})
	accented := strings.NewReplacer("c4 e4?50", "c4^ e4", "velocity: 100 60", "velocity: 100 .").Replace(mixedDynamics)
	expectNoteError(t, accented, &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960}, "chord-modifiers", "step 2, e4: after quantize, it would join c4 in a chord on step 1 and take an accent from c4", nil)
	matching := strings.NewReplacer("c4 e4?50", "c4 e4", "velocity: 100 60", "velocity: 100 .").Replace(mixedDynamics)
	expectNotesSource(t, matching, strings.Replace(matching, "c4 e4 . .", "[c4 e4] . . .", 1), &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960})
}

// A hold after a rest is silent. A note placed just before it would absorb it
// and grow; the edit writes a rest there instead, so the note keeps its length.
func TestEditNotesKeepsSilentHoldsSilent(t *testing.T) {
	source := strings.Replace(stepMoves, "[c4 e4 g4]^?70 - . . c5 . . . // keep\n  velocity: 80 . . . 100 . . .", ". . . c4 . - . . // keep", 1)
	expectNotesSource(t, source, strings.Replace(source, ". . . c4 . - . .", ". . . . c4 . . .", 1), &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960})
	// Untouched, the silent hold stays as written.
	expectNotesSource(t, source, strings.Replace(source, ". . . c4 . - . .", ". . c4 . . - . .", 1), &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{stepRef(3, 60, 1, 100)}, DT: -240})
}

// A chord that loses all but one voice keeps its comment, on a line that ends
// like the rest of the file.
func TestEditNotesKeepsChordCommentLineEndings(t *testing.T) {
	source := strings.Replace(editableChord, "  c4 e4]^?70 - - - e4 . . . }", "  c4 e4]^?70 - - - g4 . . . }", 1)
	want := strings.Replace(source, "[a3 // keep this voicing annotation\n  c4 e4]^?70", "// keep this voicing annotation\na3^?70", 1)
	expectNotesSource(t, source, want, &edit.EditNotes{Entity: "pattern:harmony", Op: "delete", Notes: []edit.NoteRef{stepRef(0, 60, 4, 100), stepRef(0, 64, 4, 100)}})
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
	expectNoteError(t, acid, &edit.EditNotes{Entity: "pattern:p", Op: "move", Notes: []edit.NoteRef{stepRef(2, 64, 1, 100)}, DT: -480}, "acid-one-pitch", "step 3, e4: move would put it on step 1 with c4; an acid pattern plays one pitch per step", nil)
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
		in         *edit.EditNotes
		code, want string
	}{
		{&edit.EditNotes{Op: "resize", Notes: []edit.NoteRef{hit}, DL: 240}, "fixed-length", "sd step 14: drum hits have a fixed length of one step"},
		{&edit.EditNotes{Op: "move", Notes: []edit.NoteRef{hit}, DP: 1}, "outside-lanes", "sd step 14: move by +1 lanes would leave the pattern's 2 lanes"},
		{&edit.EditNotes{Op: "move", Notes: []edit.NoteRef{hit}, DT: 960}, "outside-pattern", "sd step 14: move would put it on step 18, outside the pattern's 16 steps"},
		{&edit.EditNotes{Op: "quantize", Grid: 3840}, "same-cell", "bd step 4: quantize would put it on bd step 1, where another selected hit lands or stays"},
		{&edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 240, Lane: "bd", Length: 240, Velocity: 100}}}, "note-changed", "the hit at tick 240 in lane bd: a selected hit changed (no hit is there)"},
		{&edit.EditNotes{Op: "delete", Notes: []edit.NoteRef{{At: 0, Lane: "cp", Length: 240, Velocity: 100}}}, "note-changed", `the hit at tick 0 in lane cp: a selected hit changed (the pattern has no lane "cp")`},
		{&edit.EditNotes{Op: "insert", Insert: []edit.NoteRef{{At: 0, Lane: "cp", Length: 240, Velocity: 100}}}, "unknown-lane", `pattern beat has no drum lane "cp"`},
		{&edit.EditNotes{Op: "insert", Insert: []edit.NoteRef{{At: 0, Lane: "bd", Length: 480, Velocity: 100}}}, "fixed-length", "a drum hit lasts one 240-tick step, not 480 ticks"},
	} {
		c.in.Entity = "pattern:beat"
		expectNoteError(t, drumNotes, c.in, c.code, c.want, nil)
	}
	expectNoteError(t, drumNotes, &edit.EditNotes{Entity: "pattern:beat", Op: "resize", Notes: []edit.NoteRef{hit}, DL: 240}, "fixed-length", "sd step 14", map[string]any{"at": int64(3120), "step": 14, "lane": "sd"})
	if result := mustNotesEdit(t, drumNotes, &edit.EditNotes{Entity: "pattern:beat", Op: "move", Notes: []edit.NoteRef{{At: 0, Lane: "bd", Length: 240, Velocity: 100}, {At: 720, Lane: "bd", Length: 240, Velocity: 100}}}); !result.Unchanged {
		t.Fatal("a move by zero changed the pattern")
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
	expectNoteError(t, repeated, &edit.EditNotes{Entity: "pattern:p", Op: "quantize", Grid: 960}, "same-pitch", "step 6, c5: quantize would put it on step 5 as c5, where another selected c5 lands or stays", map[string]any{"at": int64(1200), "step": 6, "pitch": 72})
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

type countingCompiler struct {
	compiler edit.Compiler
	calls    *int
}

func (c countingCompiler) Compile(source []byte, files map[string][]byte) (*edit.Plan, error) {
	*c.calls++
	return c.compiler.Compile(source, files)
}

// One batch compiles the score twice, to resolve the notes and to check the
// candidate, and parses the project at most twice more: Studio's host
// ParseProject is the expensive call these counts guard.
func TestEditNotesCompilesTwiceAndParsesTheProjectAtMostTwice(t *testing.T) {
	for _, c := range []struct {
		name, source string
		in           *edit.EditNotes
		parses       int
	}{
		{"performance", notesPerformance, &edit.EditNotes{Entity: "pattern:take", Op: "quantize", Grid: 240}, 1},
		{"step pattern", stepNotes, &edit.EditNotes{Entity: "pattern:harmony", Op: "quantize", Grid: 960}, 2},
		{"drum lanes", drumNotes, &edit.EditNotes{Entity: "pattern:beat", Op: "quantize", Grid: 960}, 2},
	} {
		compiles, parses := 0, 0
		opts := edit.Options{Compiler: countingCompiler{gridCompiler{}, &compiles}, ParseProject: func(source []byte, _ map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
			parses++
			score, ds := notation.Parse(source)
			return score, ds, nil
		}}
		result, err := edit.Apply([]byte(c.source), edit.Envelope{Version: 1, Intents: []edit.Intent{c.in}}, opts)
		if err != nil || result.Unchanged {
			t.Fatalf("%s: %v", c.name, err)
		}
		if compiles != 2 || parses != c.parses {
			t.Fatalf("%s: %d compiles and %d project parses, want 2 and %d", c.name, compiles, parses, c.parses)
		}
	}
}
