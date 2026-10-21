package edit_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

const editableChord = `cicada 2
key a minor
instrument pad { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys pad {}
pattern harmony notes steps=8 { [a3 // keep this voicing annotation
  c4 e4]^?70 - - - e4 . . . }
scene verse { keys=harmony }
song { verse*2 }
// keep the ending
`

func chordEdit(t *testing.T, source []byte, intents ...edit.Intent) *edit.Result {
	t.Helper()
	result, err := edit.Apply(source, edit.Envelope{Version: 1, Revision: edit.Revision(source), Intents: intents}, edit.Options{Compiler: gridCompiler{}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSetChordPlacesAndShortensHoldsWithoutLosingAnnotations(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := []byte(strings.ReplaceAll(editableChord, "\n", newline))
		result := chordEdit(t, source, &edit.SetChord{Entity: "step:harmony/0", Notes: []int{57, 60, 64}, Hold: 2})
		want := strings.Replace(string(source), "^?70 - - - e4", "^?70 - . . e4", 1)
		if string(result.Source) != want {
			t.Fatalf("shortening changed unrelated notes or annotations:\n%s", result.Source)
		}
		step := result.Plan.Patterns[0].Data[0]
		if !reflect.DeepEqual(step.Notes, []int{57, 60, 64}) || !step.Accent || step.Probability != 70 || !result.Plan.Patterns[0].Data[1].Tie || result.Plan.Patterns[0].Data[2] != nil || result.Plan.Patterns[0].Data[4].Note != 64 {
			t.Fatal("voicing, dynamics, duration, or following onset changed")
		}
		reshaped := chordEdit(t, result.Source, &edit.SetChord{Entity: "step:harmony/0", Notes: []int{57, 60, 64, 67}, Hold: 4})
		if !bytes.Contains(reshaped.Source, []byte("// keep this voicing annotation")) || len(reshaped.Plan.Patterns[0].Data[0].Notes) != 4 || !reshaped.Plan.Patterns[0].Data[3].Tie {
			t.Fatal("reshaping lost the annotation or requested hold")
		}
	}
}

func TestChordTransformsRetainEveryVoiceAndComment(t *testing.T) {
	for _, test := range []struct {
		name   string
		intent edit.Intent
		want   []int
	}{
		{"duplicate", &edit.DuplicatePattern{Entity: "pattern:harmony", Name: "variation"}, []int{57, 60, 64}},
		{"transpose range", &edit.SetRange{Entity: "pattern:harmony", Operation: "transpose", First: 0, Last: 3, Amount: 2}, []int{59, 62, 66}},
		{"transpose step", &edit.SetStep{Entity: "step:harmony/0", Mode: "note", Pitch: 59, Accent: true, Ratchet: 1, Chance: 70}, []int{59, 62, 66}},
		{"dynamics", &edit.SetStep{Entity: "step:harmony/0", Mode: "note", Pitch: 57, Accent: false, Ratchet: 1, Chance: 90}, []int{57, 60, 64}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := chordEdit(t, []byte(editableChord), test.intent)
			p := result.Plan.Patterns[0]
			if test.name == "duplicate" {
				p = result.Plan.Patterns[1]
			}
			if !reflect.DeepEqual(p.Data[0].Notes, test.want) || !p.Data[1].Tie || !bytes.Contains(result.Source, []byte("// keep this voicing annotation")) {
				t.Fatalf("%s collapsed a voice or lost an annotation: %+v", test.name, p.Data[0])
			}
			if test.name == "dynamics" {
				if p.Data[0].Accent || p.Data[0].Probability != 90 {
					t.Fatal("chord dynamics were not updated")
				}
			} else if !p.Data[0].Accent || p.Data[0].Probability != 70 {
				t.Fatal("transform changed shared chord dynamics")
			}
		})
	}
}

func TestUnchangedChordKeepsEnharmonicSpellingAndHistory(t *testing.T) {
	source := []byte(strings.NewReplacer("a3 //", "db4 //", "  c4 e4]^?70", "  f4 ab4]^?70").Replace(editableChord))
	for _, intent := range []edit.Intent{
		&edit.SetChord{Entity: "step:harmony/0", Notes: []int{61, 65, 68}, Hold: 4},
		&edit.SetStep{Entity: "step:harmony/0", Mode: "note", Pitch: 61, Accent: true, Ratchet: 1, Chance: 70},
	} {
		result := chordEdit(t, source, intent)
		if !bytes.Equal(source, result.Source) || !result.Unchanged {
			t.Fatalf("%s rewrote unchanged chord spellings or annotations", intent.Kind())
		}
		alternative := bytes.Replace(source, []byte("^?70"), []byte("?70^"), 1)
		result = chordEdit(t, alternative, intent)
		if !bytes.Equal(alternative, result.Source) || !result.Unchanged {
			t.Fatalf("%s reordered an unchanged chord's modifiers", intent.Kind())
		}
	}
	changed := chordEdit(t, source, &edit.SetStep{Entity: "step:harmony/0", Mode: "note", Pitch: 61, Ratchet: 1, Chance: 90})
	if !bytes.Contains(changed.Source, []byte("[db4 f4 ab4]?90")) || !bytes.Contains(changed.Source, []byte("// keep this voicing annotation")) {
		t.Fatal("changing dynamics respelled a voice or dropped an annotation")
	}
}

func TestSetChordRefusesMalformedAndOutOfRangeEditsAtomically(t *testing.T) {
	source := []byte(editableChord)
	for _, in := range []*edit.SetChord{
		{Entity: "step:harmony/0", Notes: []int{60}, Hold: 1},
		{Entity: "step:harmony/0", Notes: []int{60, 60}, Hold: 1},
		{Entity: "step:harmony/0", Notes: []int{-1, 60}, Hold: 1},
		{Entity: "step:harmony/0", Notes: []int{60, 128}, Hold: 1},
		{Entity: "step:harmony/0", Notes: []int{60, 62, 64, 65, 67}, Hold: 1},
		{Entity: "step:harmony/0", Notes: []int{60, 64}, Hold: 0},
		{Entity: "step:harmony/7", Notes: []int{60, 64}, Hold: 2},
		{Entity: "step:harmony/9223372036854775807", Notes: []int{60, 64}, Hold: 2},
	} {
		before := bytes.Clone(source)
		result, err := edit.Apply(source, edit.Envelope{Version: 1, Intents: []edit.Intent{in}}, edit.Options{Compiler: gridCompiler{}})
		if err == nil || result != nil || !bytes.Equal(before, source) {
			t.Fatalf("invalid voicing changed the source: %+v %v", in, err)
		}
	}
	high := chordEdit(t, source, &edit.SetChord{Entity: "step:harmony/0", Notes: []int{120, 124, 127}, Hold: 1})
	for _, intent := range []edit.Intent{
		&edit.SetRange{Entity: "pattern:harmony", Operation: "transpose", First: 0, Last: 0, Amount: 1},
		&edit.SetStep{Entity: "step:harmony/0", Mode: "note", Pitch: 121, Ratchet: 1, Chance: 100},
	} {
		if result, err := edit.Apply(high.Source, edit.Envelope{Version: 1, Intents: []edit.Intent{intent}}, edit.Options{Compiler: gridCompiler{}}); err == nil || result != nil {
			t.Fatal("checking only the first pitch allowed a higher voice past MIDI 127")
		}
	}
}

func TestSetChordExpandsOnlyTheTargetPattern(t *testing.T) {
	source := strings.Replace(editableChord, "pattern harmony notes steps=8 { [a3 // keep this voicing annotation\n  c4 e4]^?70 - - - e4 . . . }", "phrase motif { a3 . . . }\npattern harmony notes { use motif +12 }\npattern other notes { use motif }", 1)
	result := chordEdit(t, []byte(source), &edit.SetChord{Entity: "step:harmony/0", Notes: []int{57, 60, 64}, Hold: 4, Shared: "pattern"})
	if !bytes.Contains(result.Source, []byte("phrase motif { a3 . . . }")) || !bytes.Contains(result.Source, []byte("pattern other notes { use motif }")) || !reflect.DeepEqual(result.Plan.Patterns[0].Data[0].Notes, []int{57, 60, 64}) {
		t.Fatal("placing a chord changed its shared motif or another pattern")
	}
}

func TestShortenedChordResolvesSharedTrailingHolds(t *testing.T) {
	source := strings.Replace(editableChord, "pattern harmony notes steps=8 { [a3 // keep this voicing annotation\n  c4 e4]^?70 - - - e4 . . . }", "phrase held { - - - }\npattern harmony notes { a3 use held }\npattern other notes { e3 use held }", 1)
	in := &edit.SetChord{Entity: "step:harmony/0", Notes: []int{57, 60, 64}, Hold: 1}
	result, err := edit.Apply([]byte(source), edit.Envelope{Version: 1, Intents: []edit.Intent{in}}, edit.Options{Compiler: gridCompiler{}})
	var shared *edit.SharedPhraseError
	if !errors.As(err, &shared) || result != nil {
		t.Fatalf("shortening silently edited a shared trailing hold: %v", err)
	}
	in.Shared = "pattern"
	result = chordEdit(t, []byte(source), in)
	if !bytes.Contains(result.Source, []byte("phrase held { - - - }")) || !bytes.Contains(result.Source, []byte("pattern other notes { e3 use held }")) || result.Plan.Patterns[0].Data[1] != nil || !result.Plan.Patterns[1].Data[1].Tie {
		t.Fatal("shortening altered a shared definition or another pattern's holds")
	}
}
