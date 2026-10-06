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
