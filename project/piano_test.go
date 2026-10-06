package project

import (
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestPianoScoreRuntimeAndSourceRoundtrip(t *testing.T) {
	source, err := os.ReadFile("../examples/modeled-piano.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("piano score diagnostics: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("piano project diagnostics: %+v", diagnostics)
	}
	json, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := DecodeJSON(json)
	if err != nil {
		t.Fatal(err)
	}
	regenerated, err := ToSource(roundtrip)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := notation.Parse(regenerated); len(ds) != 0 {
		t.Fatalf("regenerated piano source: %+v", ds)
	}
	cfg, err := CompileEngine(roundtrip, 48_000, 128)
	if err != nil || cfg.Track[0].Kind != engine.VoicePiano || cfg.Patterns[0].Drums != nil || cfg.Scenes[1].Settings[0].ID != kernel.ParamPianoSustain {
		t.Fatalf("piano engine configuration: %v", err)
	}
	resolved, err := ResolveParameterPath(roundtrip, "grand.sustain")
	if err != nil || resolved.ID != kernel.ParamPianoSustain || resolved.Track != 0 {
		t.Fatalf("piano sustain path: %+v %v", resolved, err)
	}
	address, err := ParamAddressByName(roundtrip, "grand.sustain")
	if err != nil || address.Param != "piano.sustain" || address.Value != float64(0) {
		t.Fatalf("piano sustain address: %+v %v", address, err)
	}
}

func TestPianoRejectsOutOfRangeNotesAndParameters(t *testing.T) {
	for _, body := range []string{"sustain = -0.1", "sustain = 1.1", "sustain = 10ms", "cutoff = 440hz", "octave = 7"} {
		source := "cicada 2\ntrack grand piano { " + body + " }\npattern p notes { c4 }\nscene s { grand=p }\nsong { s }"
		score, _ := notation.Parse([]byte(source))
		if p, _ := FromScore(score); p != nil {
			t.Fatalf("invalid piano parameter accepted: %s", body)
		}
	}
	for _, pattern := range []string{"pattern p notes { c0 }", "pattern p notes transpose=24 { b6 }"} {
		score, _ := notation.Parse([]byte("track grand piano {}\n" + pattern + "\nscene s { grand=p }\nsong { s }"))
		if p, ds := FromScore(score); p != nil {
			t.Fatal("piano accepted a note outside MIDI 21 to 108")
		} else if len(ds) == 0 {
			t.Fatal("invalid piano pitch produced no diagnostic")
		}
	}
	var tracks, bindings strings.Builder
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		tracks.WriteString("track " + name + " piano {}\n")
		bindings.WriteString(name + "=p ")
	}
	score, _ := notation.Parse([]byte(tracks.String() + "pattern p notes { c4 }\nscene s { " + bindings.String() + "}\nsong { s }"))
	if p, _ := FromScore(score); p != nil {
		t.Fatal("five eight-voice pianos exceeded the 32-voice budget")
	}
}

func TestPianoDeclarationsOverrideBuiltInSource(t *testing.T) {
	for _, test := range []struct {
		declaration, pattern string
		kind                 engine.VoiceKind
		polyphony            uint8
	}{
		{"instrument piano { voice poly { out = sine(pitch) * env(gate, 20ms) } }", "pattern p notes { [c0 e0] }", engine.VoiceGraph, 4},
		{"instrument piano { voice mono { out = sine(pitch) } }", "pattern p notes { c0 }", engine.VoiceGraph, 0},
		{"kit piano { bd = builtin.bd }", "pattern p drums { bd: x... }", engine.VoiceDrums, 0},
	} {
		source := test.declaration + "\ntrack grand piano {}\n" + test.pattern + "\nscene s { grand=p }\nsong { s }"
		score, ds := notation.Parse([]byte(source))
		if score == nil || hasErrors(ds) {
			t.Fatalf("declared piano parse: %+v", ds)
		}
		p, ds := FromScore(score)
		if p == nil {
			t.Fatalf("declared piano compile: %+v", ds)
		}
		cfg, err := CompileEngine(p, 48_000, 128)
		if err != nil || cfg.Track[0].Kind != test.kind || cfg.Track[0].Polyphony != test.polyphony {
			t.Fatalf("declared piano source changed: kind=%d polyphony=%d err=%v", cfg.Track[0].Kind, cfg.Track[0].Polyphony, err)
		}
		if _, err := ResolveParameterPath(p, "grand.sustain"); err == nil {
			t.Fatal("declared piano inherited the modeled piano's sustain control")
		}
		for _, address := range ParamAddresses(p) {
			if address.Param == "piano.sustain" {
				t.Fatal("declared piano exposed the modeled piano's control")
			}
		}
	}
	_, source, _ := assetFixture(t)
	source = []byte(strings.ReplaceAll(string(source), " hit", " piano"))
	score, ds := notation.ParseEdition(source, 2)
	if score == nil || hasErrors(ds) {
		t.Fatalf("sampler piano parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || isModeledPiano(p, "piano") {
		t.Fatalf("sampler named piano did not override the modeled source: %+v", ds)
	}
}

func TestPianoChordsCompileAndCheckEveryPitch(t *testing.T) {
	for _, pattern := range []string{"pattern p notes { [c4 e4 g4] . }", "pattern p notes transpose=24 { [c4 b6] }"} {
		score, ds := notation.Parse([]byte("track grand piano {}\n" + pattern + "\nscene s { grand=p }\nsong { s }"))
		if score == nil || hasErrors(ds) {
			t.Fatalf("piano chord parse: %+v", ds)
		}
		p, ds := FromScore(score)
		if strings.Contains(pattern, "transpose") {
			if p != nil {
				t.Fatal("piano accepted a transposed chord's upper pitch outside its keyboard")
			}
			continue
		}
		if p == nil {
			t.Fatalf("piano chord compile: %+v", ds)
		}
		cfg, err := CompileEngine(p, 48_000, 128)
		if err != nil || cfg.Track[0].Kind != engine.VoicePiano || cfg.Track[0].Polyphony != 0 || cfg.Patterns[0].Slots[0].Chords[0].Count != 3 {
			t.Fatalf("piano chord configuration: %v", err)
		}
	}
}
