package edit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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
