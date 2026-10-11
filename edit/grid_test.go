package edit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const gridScore = "title \"Studio\"\ntrack bass acid {}\ntrack drums drums {}\npattern pulse acid steps=4 { 1 . 5 . }\npattern beat drums steps=4 { bd: x... sd: .... }\nscene main { bass=pulse drums=beat }\nsong { main }\n"

type gridCompiler struct{ edition int }

func (c gridCompiler) Compile(source []byte, _ map[string][]byte) (*edit.Plan, error) {
	score, ds := notation.Parse(source)
	if c.edition != 0 {
		score, ds = notation.ParseEdition(source, c.edition)
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	return project.EditPlan(p, source), nil
}

func applyGridJSON(t *testing.T, source []byte, raw string) (*edit.Result, error) {
	t.Helper()
	var env edit.Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+raw+`]}`), &env); err != nil {
		t.Fatal(err)
	}
	return edit.Apply(source, env, edit.Options{Compiler: gridCompiler{}})
}

func TestGridExactBytesAndErrors(t *testing.T) {
	for _, c := range []struct{ raw, from, to, wantErr string }{
		{`{"kind":"togglestep","entity":"step:pulse/0"}`, "{ 1 . 5 . }", "{ . . 5 . }", ""},
		{`{"kind":"togglestep","entity":"step:beat/bd/1"}`, "bd: x...", "bd: xx..", ""},
		{`{"kind":"setpitch","entity":"step:pulse/1","pitch":60}`, "{ 1 . 5 . }", "{ 1 c4 5 . }", ""},
		{`{"kind":"togglemodifier","entity":"step:pulse/0","modifier":"accent"}`, "{ 1 . 5 . }", "{ 1^ . 5 . }", ""},
		{`{"kind":"cyclestep","entity":"step:pulse/0","field":"ratchet"}`, "{ 1 . 5 . }", "{ 1*2 . 5 . }", ""},
		{`{"kind":"setdrumvelocity","entity":"step:beat/bd/0","velocity":127}`, "bd: x...", "bd: X...", ""},
		{`{"kind":"togglestep","entity":"step:pulse/8"}`, "", "", "pattern pulse has no step 9 in lane "},
		{`{"kind":"togglemodifier","entity":"step:pulse/1","modifier":"accent"}`, "", "", "step 2 needs a note before setting accent"},
		{`{"kind":"setpitch","entity":"step:pulse/0","pitch":128}`, "", "", "a note pattern, nonnegative step, and MIDI pitch 0–127 are required"},
	} {
		t.Run(c.raw, func(t *testing.T) {
			before := []byte(gridScore)
			got, err := applyGridJSON(t, before, c.raw)
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Replace(gridScore, c.from, c.to, 1); string(got.Source) != want {
				t.Fatalf("bytes %q, want %q", got.Source, want)
			}
			if !bytes.Equal(before, []byte(gridScore)) {
				t.Fatal("input mutated")
			}
		})
	}
}

func TestSetPitchJSONRequiresPitch(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"setpitch","entity":"step:pulse/0","shared":"pattern"}`,
		`{"kind":"setpitch","entity":"step:pulse/0","shared":"pattern","pitch":null}`,
	} {
		_, err := applyGridJSON(t, []byte(gridScore), raw)
		if err == nil || err.Error() != "choose a pitch" {
			t.Fatalf("missing pitch: %v", err)
		}
		var env edit.Envelope
		if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+raw+`]}`), &env); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		_, err = edit.Apply([]byte(gridScore), env, edit.Options{Compiler: gridCompiler{}})
		if err == nil || err.Error() != "choose a pitch" {
			t.Fatalf("roundtrip turned missing pitch into MIDI zero: %v", err)
		}
	}
	got, err := applyGridJSON(t, []byte(gridScore), `{"kind":"setpitch","entity":"step:pulse/0","pitch":0}`)
	if err != nil || !strings.Contains(string(got.Source), "c0,") {
		t.Fatalf("MIDI zero is valid: %+v, %v", got, err)
	}
}

func TestSharedTransposedChordPreservesPitchesAndModifiers(t *testing.T) {
	for _, chord := range []string{"[d4 f4 a4]^?70", "[d4  f4\t a4] ^?70", "[d4 // keep chord color\n f4 a4]^?70"} {
		for _, policy := range []string{"definition", "detach", "pattern"} {
			for _, newline := range []string{"\n", "\r\n"} {
				t.Run(fmt.Sprintf("%s/%s/%q", policy, chord, newline), func(t *testing.T) {
					source := "instrument piano { voice poly { out=sine(pitch)*env(gate,300ms) } }\ntrack keys piano {}\nphrase harmony { " + chord + " c4 }\npattern chords notes { . use harmony +12 use harmony +24 }\nscene main { keys=chords }\nsong { main }\n"
					source = strings.ReplaceAll(source, "\n", newline)
					got, err := applyGridJSON(t, []byte(source), `{"kind":"togglemodifier","entity":"step:chords/1","modifier":"accent","shared":"`+policy+`"}`)
					if err != nil {
						t.Fatal(err)
					}
					authoredChord := strings.ReplaceAll(chord, "\n", newline)
					first := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(authoredChord, "d4", "d5"), "f4", "f5"), "a4", "a5")
					first = strings.Replace(first, "^", "", 1)
					want := source
					switch policy {
					case "definition":
						want = strings.Replace(source, authoredChord, strings.Replace(authoredChord, "^", "", 1), 1)
					case "detach":
						want = strings.Replace(source, "use harmony +12", first+" c5", 1)
					case "pattern":
						second := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(authoredChord, "d4", "d6"), "f4", "f6"), "a4", "a6")
						want = strings.Replace(strings.Replace(source, "use harmony +12", first+" c5", 1), "use harmony +24", second+" c6", 1)
					}
					if string(got.Source) != want {
						t.Fatalf("expansion dropped chord bytes\n got: %s\nwant: %s", got.Source, want)
					}
					plan := got.Plan
					for _, p := range plan.Patterns {
						if p.ID != "chords" {
							continue
						}
						for i, pitches := range map[int][]int{1: {74, 77, 81}, 3: {86, 89, 93}} {
							step := p.Data[i]
							accent := i == 3 && policy != "definition"
							if step == nil || !reflect.DeepEqual(step.Notes, pitches) || step.Accent != accent || step.Slide || step.Ratchet != 1 || step.Probability != 70 {
								t.Fatalf("%s chord %d lost pitches/modifiers: %+v", policy, i, step)
							}
						}
					}
				})
			}
		}
	}
}
