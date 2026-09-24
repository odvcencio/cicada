package project

import (
	"os"
	"path/filepath"
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
