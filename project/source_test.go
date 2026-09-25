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
			source, err := os.ReadFile(filepath.Join("..", "examples", name+".cicada"))
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
