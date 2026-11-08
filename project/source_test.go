package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestSourceRoundTripExamples(t *testing.T) {
	for _, name := range []string{"first-acid", "circuit-kit", "glassbass", "acid-voice"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "testdata", "edition1", "examples", name+".cicada"))
			if err != nil {
				t.Fatal(err)
			}
			score, diagnostics := notation.Parse(source)
			if len(diagnostics) != 0 {
				t.Fatalf("parse: %+v", diagnostics)
			}
			project, diagnostics := FromScore(score)
			if project == nil {
				t.Fatalf("compile: %+v", diagnostics)
			}
			rewritten, err := ToSource(project)
			if err != nil {
				t.Fatal(err)
			}
			if len(rewritten) == 0 {
				t.Fatal("empty source")
			}
		})
	}
}

func TestSourceRoundTripPreservesTypedLetLiteral(t *testing.T) {
	source := []byte(`cicada 1
instrument osc { voice mono { let freq = 48hz; out = sine(freq); } }
track t osc {}
pattern p notes steps=1 { 1 }
scene s { t=p }
song { s }
`)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	compiled, diagnostics := FromScore(score)
	if compiled == nil {
		t.Fatalf("compile: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(compiled)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, diagnostics := notation.Parse(rewritten)
	if len(diagnostics) != 0 {
		t.Fatalf("generated source: %+v\n%s", diagnostics, rewritten)
	}
	recompiled, diagnostics := FromScore(reparsed)
	if recompiled == nil {
		t.Fatalf("recompile: %+v", diagnostics)
	}
	if !reflect.DeepEqual(decoded, recompiled) {
		t.Fatal("typed let literal changed in the source/JSON/source round trip")
	}
}

// A let that multiplies a unit literal by a unitless expression keeps the
// literal's unit when the binding is used more than once.
func TestSourceRoundTripPreservesReusedUnitProduct(t *testing.T) {
	source := []byte(`cicada 2
instrument drum {
  voice mono {
    let f = 147Hz * exp2(env(gate, 30ms) * 0.06)
    out = sine(f) * env(gate, 400ms) * 0.8 + sine(f * 1.594) * env(gate, 170ms) * 0.3
  }
}
track t drum {}
pattern p notes { c4 . . . }
scene s { t = p }
song { s }
`)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	compiled, diagnostics := FromScore(score)
	if compiled == nil {
		t.Fatalf("compile: %+v", diagnostics)
	}
	if err := ValidateProject(compiled); err != nil {
		t.Fatalf("validate: %v", err)
	}
	encoded, err := CanonicalJSON(compiled)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, diagnostics := notation.Parse(rewritten)
	if len(diagnostics) != 0 {
		t.Fatalf("generated source: %+v\n%s", diagnostics, rewritten)
	}
	recompiled, diagnostics := FromScore(reparsed)
	if recompiled == nil {
		t.Fatalf("recompile: %+v\n%s", diagnostics, rewritten)
	}
	if !reflect.DeepEqual(decoded, recompiled) {
		t.Fatalf("reused unit product changed in the round trip:\n%s", rewritten)
	}
}

// Ring modulation: a let that multiplies two audio signals round-trips with
// both operands kept as audio.
func TestSourceRoundTripPreservesAudioProduct(t *testing.T) {
	source := []byte(`cicada 2
instrument ratchet {
  voice mono {
    let ridge = sine(pitch) * sine(pitch)
    out = highpass(noise() * ridge, 1500Hz) * env(gate, 400ms) + sine(pitch) * ridge * 0.1
  }
}
track t ratchet {}
pattern p notes { e2 . . . }
scene s { t = p }
song { s }
`)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	compiled, diagnostics := FromScore(score)
	if compiled == nil {
		t.Fatalf("compile: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(compiled)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, diagnostics := notation.Parse(rewritten)
	if len(diagnostics) != 0 {
		t.Fatalf("generated source: %+v\n%s", diagnostics, rewritten)
	}
	recompiled, diagnostics := FromScore(reparsed)
	if recompiled == nil || !reflect.DeepEqual(decoded, recompiled) {
		t.Fatalf("audio product changed in the round trip: %+v\n%s", diagnostics, rewritten)
	}
}

// A let that a call argument types (sine(f) needs Hz) keeps that type against
// a later use inside arithmetic, whichever comes first, and lets that main
// already wrote correctly keep their source.
func TestSourceRoundTripKeepsAForcedLetTypeAgainstAGuess(t *testing.T) {
	for _, body := range []string{
		"let amt = 0.5 * velocity\n    out = amt * sine(pitch + amt * 200Hz)",
		"let amt = 0.5 * velocity\n    out = sine(pitch + amt * 200Hz) * amt",
		"let f = 147Hz * exp2(env(gate, 30ms) * 0.06)\n    out = sine(f * 1.594) * 0.3 + sine(f) * env(gate, 400ms)",
		"let f = 147Hz * exp2(env(gate, 30ms) * 0.06)\n    out = sine(f) * env(gate, 400ms) + sine(f * 1.594) * 0.3",
		"let a = 100Hz\n    let b = 50Hz\n    out = sine(a) * 0.5 + sine((a + b) * 2) * 0.5",
		"let t = 30ms\n    let f = 147Hz * exp2(env(gate, t) * 0.06)\n    out = delay(sine(pitch), t * 2) + sine(f * 1.5)",
		"let t = 30ms\n    let f = 147Hz * exp2(env(gate, t) * 0.06)\n    out = sine(f) + delay(sine(pitch), t * 2) + sine(f * 1.5)",
	} {
		source := []byte("cicada 2\ninstrument drum {\n  voice mono {\n    " + body + "\n  }\n}\ntrack t drum {}\npattern p notes { c4 . . . }\nscene s { t = p }\nsong { s }\n")
		score, diagnostics := notation.Parse(source)
		if len(diagnostics) != 0 {
			t.Fatalf("parse: %+v\n%s", diagnostics, source)
		}
		compiled, diagnostics := FromScore(score)
		if compiled == nil {
			t.Fatalf("compile: %+v\n%s", diagnostics, source)
		}
		rewritten, err := ToSource(compiled)
		if err != nil {
			t.Fatal(err)
		}
		reparsed, diagnostics := notation.Parse(rewritten)
		if len(diagnostics) != 0 {
			t.Fatalf("generated source: %+v\n%s", diagnostics, rewritten)
		}
		recompiled, diagnostics := FromScore(reparsed)
		if recompiled == nil {
			t.Fatalf("recompile: %+v\n%s", diagnostics, rewritten)
		}
		if err := ValidateProject(recompiled); err != nil {
			t.Fatalf("validate: %v\n%s", err, rewritten)
		}
		if !reflect.DeepEqual(compiled.Instruments, recompiled.Instruments) {
			t.Fatalf("instrument changed in the round trip:\n%s", rewritten)
		}
	}
}
