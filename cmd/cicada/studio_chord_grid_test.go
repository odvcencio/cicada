package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const studioChordScore = `tempo 120
key d minor
instrument piano { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys piano {}
pattern harmony notes gate=75 { [d4 f4 a4]^?70 - . c4 }
scene verse { keys=harmony }
song { verse }
`

func chordGridProject(t *testing.T, source []byte) *project.Project {
	t.Helper()
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		t.Fatalf("parse: %+v\n%s", diagnostics, source)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(diagnostics) {
		t.Fatalf("compile: %+v\n%s", diagnostics, source)
	}
	return p
}

func TestChordGridPitchEditsKeepOtherPitchesAndExactSource(t *testing.T) {
	for _, tt := range []struct {
		name, before, after string
		pitch               int
		want                []int
	}{
		{"two first", "[d4 f4]", "f4", 62, []int{65}},
		{"two last", "[d4 f4]", "d4", 65, []int{62}},
		{"three first", "[d4 f4 a4]", "[f4 a4]", 62, []int{65, 69}},
		{"three middle", "[d4 f4 a4]", "[d4 a4]", 65, []int{62, 69}},
		{"three last", "[d4 f4 a4]", "[d4 f4]", 69, []int{62, 65}},
		{"four middle", "[d4 f4 a4 c5]", "[d4 a4 c5]", 65, []int{62, 69, 72}},
		{"four first", "[d4 f4 a4 c5]", "[f4 a4 c5]", 62, []int{65, 69, 72}},
		{"add third", "[d4 f4]", "[d4 f4 a4]", 69, []int{62, 65, 69}},
		{"add fourth", "[d4 f4 a4]", "[d4 f4 a4 c5]", 72, []int{62, 65, 69, 72}},
		{"preserve comment when removing pitch", "[d4 // chord comment\n f4 a4]", "[d4 // chord comment\n a4]", 65, []int{62, 69}},
		{"comment between pitches on add", "[d4 // chord ] comment\n f4]", "[d4 // chord ] comment\n f4 a4]", 69, []int{62, 65, 69}},
		{"preserve spelling and spacing", "[ d4\tf4  a4 ]", "[ d4\ta4 ]", 65, []int{62, 69}},
		{"preserve surrounding spaces on add", "[ d4\tf4  ]", "[ d4\tf4 a4  ]", 69, []int{62, 65, 69}},
		{"preserve flat spellings", "[db4 f4 ab4]", "[db4 ab4]", 65, []int{61, 68}},
		{"unsorted authored pitches", "[a4 d4 f4]", "[a4 f4]", 62, []int{69, 65}},
		{"preserve degree and octave spelling", "[1'' f4 a4]", "[1'' a4]", 65, []int{62, 69}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := []byte(strings.Replace(studioChordScore, "[d4 f4 a4]", tt.before, 1))
			updated, err := pitchedSource(source, "harmony", "", 0, tt.pitch)
			wantSource := strings.Replace(string(source), tt.before, tt.after, 1)
			if err != nil || string(updated) != wantSource {
				t.Fatalf("got %s, %v; want %s", updated, err, wantSource)
			}
			p := chordGridProject(t, updated)
			pattern, step := p.Patterns[0], p.Patterns[0].Data[0]
			if got := viewStepPitches(step); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("pitches %v, want %v", got, tt.want)
			}
			if !step.Accent || step.Probability != 70 || step.Ratchet != 1 || step.Slide || pattern.GatePercent != 75 || !pattern.Data[1].Tie || pattern.Data[2] != nil {
				t.Fatalf("shared modifiers, tie, gate or rest changed: %+v %+v", pattern, step)
			}
			// Edits remain compatible with the semantic source roundtrip.
			roundtrip, err := project.ToSource(p)
			if err != nil {
				t.Fatal(err)
			}
			again := chordGridProject(t, roundtrip)
			before, _ := json.Marshal(p)
			after, _ := json.Marshal(again)
			if !bytes.Equal(before, after) {
				t.Fatalf("source roundtrip changed project\n%s\n%s", before, after)
			}
		})
	}
}

func TestChordGridRepeatedAndTransposedPhraseEdits(t *testing.T) {
	source := []byte(strings.Replace(studioChordScore, "pattern harmony notes gate=75 { [d4 f4 a4]^?70 - . c4 }", "phrase hook { [d4 f4 a4]^?70 - }\npattern harmony notes gate=75 { use hook use hook +7 }", 1))
	original := append([]byte(nil), source...)
	for i := 0; i < 4; i++ {
		var err error
		source, err = pitchedSource(source, "harmony", "", 2, 79) // Add C5 through the +7 use.
		if err != nil || !bytes.Contains(source, []byte("[d4 f4 a4 c5]^?70")) {
			t.Fatalf("add through phrase: %s %v", source, err)
		}
		p := chordGridProject(t, source)
		if !reflect.DeepEqual(p.Patterns[0].Data[0].Notes, []int{62, 65, 69, 72}) || !reflect.DeepEqual(p.Patterns[0].Data[2].Notes, []int{69, 72, 76, 79}) {
			t.Fatalf("shared uses did not retain transpose: %+v", p.Patterns[0].Data)
		}
		source, err = pitchedSource(source, "harmony", "", 2, 79)
		if err != nil || !bytes.Equal(source, original) {
			t.Fatalf("repeat roundtrip: %s %v", source, err)
		}
	}
	removed, err := pitchedSource(source, "harmony", "", 2, 72) // Middle F4 through +7.
	if err != nil || !bytes.Contains(removed, []byte("[d4 a4]^?70")) {
		t.Fatalf("remove through phrase: %s %v", removed, err)
	}
	if updated, err := pitchedSource(source, "harmony", "", 2, 0); err == nil || updated != nil {
		t.Fatal("accepted unrepresentable phrase pitch")
	}
}

func TestChordGridProjectsAllPitchesAndExplicitActions(t *testing.T) {
	for count := 2; count <= 4; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			pitches := []int{62, 65, 69, 72}[:count]
			spellings := []string{"d4", "f4", "a4", "c5"}[:count]
			source := strings.Replace(studioChordScore, "[d4 f4 a4]", "["+strings.Join(spellings, " ")+"]", 1)
			handler, err := studioHandler(studioTestPath(t, source))
			if err != nil {
				t.Fatal(err)
			}
			page := studioCall(t, handler, "/", nil)
			if page.Code != http.StatusOK {
				t.Fatal(page.Body.String())
			}
			p := chordGridProject(t, []byte(source))
			rows := pitchRows(p.Patterns[0].Data, 4)
			var active []int
			for _, row := range rows {
				if row.Cells[0].On {
					active = append([]int{row.Note}, active...)
				}
				if row.Cells[1].On || row.Cells[2].On {
					t.Fatal("tie/rest projected as a new pitch")
				}
			}
			if !reflect.DeepEqual(active, pitches) {
				t.Fatalf("active rows %v, want %v", active, pitches)
			}
			for _, pitch := range pitches {
				label := fmt.Sprintf(`data-pitch="%d" aria-label="Remove %s from harmony step 1 chord"`, pitch, midiNote(uint8(pitch)))
				if !strings.Contains(page.Body.String(), label) {
					t.Fatalf("missing pitch action %s", label)
				}
			}
			for _, label := range []string{`aria-label="Add D♯4 to harmony step 1 chord"`, `aria-label="Clear chord to rest in harmony Step 1, chord`, `single remaining note uses normal set/clear editing`} {
				if !strings.Contains(page.Body.String(), label) {
					t.Fatalf("missing honest chord UI %s", label)
				}
			}
			cell := makeViewCell(0, p.Patterns[0].Data[0], false)
			for _, pitch := range pitches {
				if !strings.Contains(cell.Note, midiNote(uint8(pitch))) {
					t.Fatalf("summary omitted %d: %+v", pitch, cell)
				}
			}
		})
	}
}

func TestChordGridHTTPUndoRedoClearAndRejectedWrites(t *testing.T) {
	source := strings.Replace(studioChordScore, "[d4 f4 a4]", "[d4 f4 a4 c5]", 1)
	path := studioTestPath(t, source)
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pitch := 74
	full := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(read()), Pattern: "harmony", Step: 0, Pitch: &pitch})
	if full.Code != http.StatusUnprocessableEntity || !strings.Contains(full.Body.String(), "remove a pitch before adding another") || string(read()) != source || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatalf("fifth pitch must fail visibly and atomically: %d %s", full.Code, full.Body.String())
	}
	pitch = 65
	result := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(read()), Pattern: "harmony", Step: 0, Pitch: &pitch})
	if result.Code != http.StatusOK {
		t.Fatal(result.Body.String())
	}
	edited := read()
	if !bytes.Contains(edited, []byte("[d4 a4 c5]^?70 -")) {
		t.Fatalf("wrong edited chord: %s", edited)
	}
	stale := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision([]byte(source)), Pattern: "harmony", Step: 0, Pitch: &pitch})
	if stale.Code != http.StatusConflict || !bytes.Equal(read(), edited) {
		t.Fatal("stale chord edit mutated score")
	}
	for _, action := range []string{"undo", "redo"} {
		response := studioCall(t, handler, "/api/"+action, studioEdit{Revision: studioRevision(read())})
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		want := []byte(source)
		if action == "redo" {
			want = edited
		}
		if !bytes.Equal(read(), want) {
			t.Fatalf("%s changed exact source", action)
		}
	}
	clear := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(read()), Pattern: "harmony", Step: 0})
	if clear.Code != http.StatusOK || !bytes.Contains(read(), []byte("gate=75 { . - . c4 }")) {
		t.Fatalf("explicit whole chord clear failed: %s", clear.Body.String())
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(read())})
	if undo.Code != http.StatusOK || !bytes.Equal(read(), edited) {
		t.Fatal("undo clear lost chord")
	}
}

func TestChordGridSingletonKeepsMonoSetClearBehavior(t *testing.T) {
	source := []byte(strings.Replace(studioChordScore, "[d4 f4 a4]", "[d4 f4]", 1))
	var err error
	for _, edit := range []struct {
		pitch int
		token string
	}{{62, "f4^?70"}, {69, "a4^?70"}, {69, "."}, {62, "d4"}} {
		source, err = pitchedSource(source, "harmony", "", 0, edit.pitch)
		if err != nil || !bytes.Contains(source, []byte("{ "+edit.token+" - . c4 }")) {
			t.Fatalf("mono after singleton: %s %v", source, err)
		}
		step := chordGridProject(t, source).Patterns[0].Data[0]
		if step != nil && len(step.Notes) > 0 {
			t.Fatal("mono step implicitly promoted to chord")
		}
	}
}

func TestChordGridRecordingOverChordRefusedAtomically(t *testing.T) {
	for _, pitch := range []int{62, 65, 72} { // Root, matching middle tone, and new tone.
		t.Run(fmt.Sprint(pitch), func(t *testing.T) {
			path := studioTestPath(t, studioChordScore)
			handler, err := studioHandler(path)
			if err != nil {
				t.Fatal(err)
			}
			result := studioCall(t, handler, "/api/record", studioEdit{Revision: studioRevision([]byte(studioChordScore)), Track: "keys", Pattern: "harmony", Take: []studioTakeNote{{Tick: 0, EndTick: 60, Note: pitch, Velocity: 90}}})
			got, err := os.ReadFile(path)
			if err != nil || result.Code != http.StatusUnprocessableEntity || !strings.Contains(result.Body.String(), "is not an acid track") || string(got) != studioChordScore || len(studioHistoryEdits(t, handler)) != 0 {
				t.Fatalf("recording was not refused atomically: %d %s %v", result.Code, result.Body.String(), err)
			}
		})
	}
}

func TestChordGridMIDIExtremesAddRemove(t *testing.T) {
	for _, tt := range []struct {
		pitch    int
		spelling string
	}{{0, "c0,"}, {127, "g6'''"}} {
		source := []byte(studioChordScore)
		added, err := pitchedSource(source, "harmony", "", 0, tt.pitch)
		if err != nil || !bytes.Contains(added, []byte("[d4 f4 a4 "+tt.spelling+"]^?70")) {
			t.Fatalf("add extreme: %s %v", added, err)
		}
		p := chordGridProject(t, added)
		if p.Patterns[0].Data[0].Notes[3] != tt.pitch {
			t.Fatal("wrong extreme pitch")
		}
		removed, err := pitchedSource(added, "harmony", "", 0, tt.pitch)
		if err != nil || !bytes.Equal(removed, source) {
			t.Fatalf("remove extreme: %s %v", removed, err)
		}
	}
}

func TestChordGridSingletonSpacedSharedModifiers(t *testing.T) {
	for _, spacing := range []string{" ", "\t", "\n", " // modifier\n "} {
		t.Run(spacing, func(t *testing.T) {
			source := []byte(strings.Replace(studioChordScore, "[d4 f4 a4]^?70", "[d4 f4] ^"+spacing+"?70", 1))
			chordGridProject(t, source)
			updated, err := pitchedSource(source, "harmony", "", 0, 62)
			if err != nil {
				t.Fatal(err)
			}
			replacement := "f4^?70"
			if strings.Contains(spacing, "//") {
				replacement = "// modifier\nf4^?70"
			}
			if !bytes.Equal(updated, []byte(strings.Replace(string(source), "[d4 f4] ^"+spacing+"?70", replacement, 1))) {
				t.Fatalf("unexpected singleton source change: %s", updated)
			}
			step := chordGridProject(t, updated).Patterns[0].Data[0]
			if step.Note != 65 || !step.Accent || step.Probability != 70 {
				t.Fatalf("singleton lost shared modifiers: %+v", step)
			}
		})
	}
}
