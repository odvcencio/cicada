package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

func applyParityIntent(t *testing.T, source []byte, raw string, opts edits.Options) (*edits.Result, error) {
	t.Helper()
	var env edits.Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+raw+`]}`), &env); err != nil {
		t.Fatal(err)
	}
	return edits.Apply(source, env, opts)
}

func assertWriterParity(t *testing.T, source []byte, raw, label string, old func([]byte) ([]byte, error)) {
	t.Helper()
	want, oldErr := old(source)
	path := studioTestPath(t, string(source))
	got, err := applyParityIntent(t, source, raw, edits.Options{Compiler: &studioCompiler{path: path}})
	if oldErr != nil {
		if err == nil || err.Error() != oldErr.Error() {
			t.Fatalf("intent error %v, old writer error %v", err, oldErr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Source, want) || got.Label != label {
		t.Fatalf("intent bytes/label %q / %q, old writer %q / %q", got.Source, got.Label, want, label)
	}
}

func TestGridWriterParity(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, c := range []struct {
			name, raw string
			body      studioEdit
			old       func([]byte) ([]byte, error)
		}{
			{"toggle", `{"kind":"togglestep","entity":"step:pulse/0"}`, studioEdit{Pattern: "pulse"}, func(s []byte) ([]byte, error) { return toggledSource(s, "pulse", "", 0) }},
			{"drum toggle", `{"kind":"togglestep","entity":"step:beat/bd/1"}`, studioEdit{Pattern: "beat", Lane: "bd", Step: 1}, func(s []byte) ([]byte, error) { return toggledSource(s, "beat", "bd", 1) }},
			{"pitch", `{"kind":"setpitch","entity":"step:pulse/1","pitch":60}`, studioEdit{Pattern: "pulse", Step: 1, Pitch: gridParityPitch(60)}, func(s []byte) ([]byte, error) { return pitchedSource(s, "pulse", "", 1, 60) }},
			{"accent", `{"kind":"togglemodifier","entity":"step:pulse/0","modifier":"accent"}`, studioEdit{Pattern: "pulse", Modifier: "accent"}, func(s []byte) ([]byte, error) { return toggledModifierSource(s, "pulse", "", 0, "accent") }},
			{"slide", `{"kind":"togglemodifier","entity":"step:pulse/0","modifier":"slide"}`, studioEdit{Pattern: "pulse", Modifier: "slide"}, func(s []byte) ([]byte, error) { return toggledModifierSource(s, "pulse", "", 0, "slide") }},
			{"ratchet", `{"kind":"cyclestep","entity":"step:pulse/0","field":"ratchet"}`, studioEdit{Pattern: "pulse", Modifier: "ratchet"}, func(s []byte) ([]byte, error) { return cycledStepSource(s, "pulse", "", 0, "ratchet") }},
			{"chance", `{"kind":"cyclestep","entity":"step:pulse/0","field":"chance"}`, studioEdit{Pattern: "pulse", Modifier: "chance"}, func(s []byte) ([]byte, error) { return cycledStepSource(s, "pulse", "", 0, "chance") }},
			{"velocity", `{"kind":"setdrumvelocity","entity":"step:beat/bd/0","velocity":127}`, studioEdit{Pattern: "beat", Lane: "bd", Modifier: "velocity"}, func(s []byte) ([]byte, error) { return drumVelocitySource(s, "beat", "bd", 0, 127) }},
			{"outside", `{"kind":"togglestep","entity":"step:pulse/8"}`, studioEdit{}, func(s []byte) ([]byte, error) { return toggledSource(s, "pulse", "", 8) }},
			{"rest accent", `{"kind":"togglemodifier","entity":"step:pulse/1","modifier":"accent"}`, studioEdit{}, func(s []byte) ([]byte, error) { return toggledModifierSource(s, "pulse", "", 1, "accent") }},
			{"drum pitch", `{"kind":"setpitch","entity":"step:beat/bd/0","pitch":60}`, studioEdit{}, func(s []byte) ([]byte, error) { return pitchedSource(s, "beat", "bd", 0, 60) }},
			{"negative", `{"kind":"togglestep","entity":"step:pulse/-1"}`, studioEdit{}, func(s []byte) ([]byte, error) { return toggledSource(s, "pulse", "", -1) }},
			{"missing", `{"kind":"togglestep","entity":"step:missing/0"}`, studioEdit{}, func(s []byte) ([]byte, error) { return toggledSource(s, "missing", "", 0) }},
			{"bad modifier", `{"kind":"togglemodifier","entity":"step:pulse/0","modifier":"bad"}`, studioEdit{}, func(s []byte) ([]byte, error) { return toggledModifierSource(s, "pulse", "", 0, "bad") }},
			{"rest cycle", `{"kind":"cyclestep","entity":"step:pulse/1","field":"ratchet"}`, studioEdit{}, func(s []byte) ([]byte, error) { return cycledStepSource(s, "pulse", "", 1, "ratchet") }},
			{"rest velocity", `{"kind":"setdrumvelocity","entity":"step:beat/bd/1","velocity":100}`, studioEdit{}, func(s []byte) ([]byte, error) { return drumVelocitySource(s, "beat", "bd", 1, 100) }},
			{"bad velocity", `{"kind":"setdrumvelocity","entity":"step:beat/bd/0","velocity":0}`, studioEdit{}, func(s []byte) ([]byte, error) { return drumVelocitySource(s, "beat", "bd", 0, 0) }},
		} {
			t.Run(fmt.Sprintf("%s/%q", c.name, newline), func(t *testing.T) {
				source := bytes.ReplaceAll([]byte(studioScore), []byte("\n"), []byte(newline))
				assertWriterParity(t, source, c.raw, studioEditLabel(c.body), c.old)
			})
		}
	}
}

func gridParityPitch(pitch int) *int { return &pitch }

func TestGridSharedWriterParity(t *testing.T) {
	for _, policy := range []string{"definition", "detach", "pattern"} {
		for _, c := range []struct {
			raw  string
			body studioEdit
			old  func([]byte) ([]byte, error)
		}{
			{`"kind":"togglestep"`, studioEdit{Pattern: "p", Step: 4}, func(s []byte) ([]byte, error) { return toggledSource(s, "p", "", 4) }},
			{`"kind":"setpitch","pitch":72`, studioEdit{Pattern: "p", Step: 4, Pitch: gridParityPitch(72)}, func(s []byte) ([]byte, error) { return pitchedSource(s, "p", "", 4, 72) }},
			{`"kind":"togglemodifier","modifier":"accent"`, studioEdit{Pattern: "p", Step: 4, Modifier: "accent"}, func(s []byte) ([]byte, error) { return toggledModifierSource(s, "p", "", 4, "accent") }},
			{`"kind":"cyclestep","field":"ratchet"`, studioEdit{Pattern: "p", Step: 4, Modifier: "ratchet"}, func(s []byte) ([]byte, error) { return cycledStepSource(s, "p", "", 4, "ratchet") }},
		} {
			t.Run(policy+"/"+c.raw, func(t *testing.T) {
				old := func(s []byte) ([]byte, error) {
					if policy == "detach" || policy == "pattern" {
						var err error
						s, err = independentPatternSource(s, "p")
						if err != nil {
							return nil, err
						}
						if policy == "detach" {
							s = bytes.Replace(s, []byte("swing = 54% 1^ . 5~*2?70 -"), []byte("swing = 54% use hook |"), 1)
						}
					}
					return c.old(s)
				}
				assertWriterParity(t, []byte(studioPatternScore), `{`+c.raw+`,"entity":"step:p/4","shared":"`+policy+`"}`, studioEditLabel(c.body), old)
			})
		}
	}
	mixed := []byte("track bass acid {}\nphrase riff { 1 . 5 . }\npattern pulse acid { 1 . use riff }\nscene main { bass=pulse }\nsong { main }\n")
	assertWriterParity(t, mixed, `{"kind":"togglestep","entity":"step:pulse/0","shared":"pattern"}`, "Grid · pulse step 1 toggled", func(s []byte) ([]byte, error) {
		local, err := independentPatternSource(s, "pulse")
		if err != nil {
			return nil, err
		}
		return toggledSource(local, "pulse", "", 0)
	})
	for _, pitch := range []int{62, 64} {
		for _, steps := range []string{"[d4 f4 a4]^?70", "use harmony"} {
			source := []byte("instrument piano { voice poly { out=sine(pitch)*env(gate,300ms) } }\ntrack keys piano {}\nphrase harmony { [d4 f4 a4]^?70 }\npattern chords notes { " + steps + " }\nscene main { keys=chords }\nsong { main }\n")
			t.Run(fmt.Sprintf("chord/%s/%d", steps, pitch), func(t *testing.T) {
				assertWriterParity(t, source, fmt.Sprintf(`{"kind":"setpitch","entity":"step:chords/0","pitch":%d,"shared":"definition"}`, pitch), "Grid · chords step 1 pitch changed", func(s []byte) ([]byte, error) { return pitchedSource(s, "chords", "", 0, pitch) })
			})
		}
	}
}
