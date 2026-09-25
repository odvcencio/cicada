package project

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func TestInstrumentHomeOctaveAndTrackOverride(t *testing.T) {
	src := []byte(`cicada 1
key c minor
instrument bass { octave = 3; voice mono { out = saw(pitch); } }
track low bass {}
track high bass { octave = 4 }
phrase motif { c 1' c#5 }
pattern lowline notes steps=3 { use motif }
pattern highline notes steps=3 { use motif }
scene main { low=lowline high=highline }
song { main }
`)
	score, diagnostics := notation.Parse(src)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	for i, want := range [][]uint8{{48, 60, 73}, {60, 72, 73}} {
		compiled, err := CompilePattern(score, score.Patterns[i], score.Tracks[i])
		if err != nil {
			t.Fatal(err)
		}
		for stepIndex, note := range want {
			step, err := seq.UnpackStep(compiled[0].Pattern.Steps[stepIndex])
			if err != nil || step.Note != note {
				t.Fatalf("track %d step %d: note %d, err %v; want %d", i, stepIndex, step.Note, err, note)
			}
		}
	}
	p, diagnostics := FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("compile: %+v", diagnostics)
	}
	if p.Instruments[0].Octave == nil || *p.Instruments[0].Octave != 3 {
		t.Fatalf("home octave missing: %+v", p.Instruments[0])
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(p, decoded) {
		t.Fatalf("JSON round trip: %v", err)
	}
	if _, err := CompileEngine(decoded, 48000, 128); err != nil {
		t.Fatalf("runtime: %v", err)
	}
}

func TestLegacyInstrumentJSONDefaultsToOctaveTwo(t *testing.T) {
	p, diagnostics := FromScore(firstScore(t))
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("compile: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	for _, item := range raw["instruments"].([]any) {
		delete(item.(map[string]any), "octave")
	}
	legacy, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(legacy)
	if err != nil || !reflect.DeepEqual(p, decoded) {
		t.Fatalf("legacy default: %v", err)
	}
	invalid := bytes.Replace(legacy, []byte(`"mode":"mono"`), []byte(`"octave":null,"mode":"mono"`), 1)
	if bytes.Equal(invalid, legacy) {
		t.Fatal("test fixture did not contain mono mode")
	}
	if _, err := DecodeJSON(invalid); err == nil {
		t.Fatal("null octave accepted")
	}
}

func TestInstrumentOctaveRange(t *testing.T) {
	for _, octave := range []string{"7", "-1"} {
		source := []byte("cicada 1\ninstrument bass { octave = " + octave + " voice mono { out = saw(pitch) } }\ntrack low bass {}\npattern p notes steps=1 { c }\nscene main { low=p }\nsong { main }\n")
		_, diagnostics := notation.Parse(source)
		if len(diagnostics) == 0 {
			t.Fatalf("instrument octave %s accepted", octave)
		}
	}
}
