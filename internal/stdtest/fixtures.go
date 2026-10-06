// Package stdtest loads the std examples and their independent inline fixtures.
package stdtest

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var Libraries = []string{"synth", "drums", "fx", "presets"}

func Load(t testing.TB, repository, name string) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	score, ds, err := project.LoadScore(filepath.Join(repository, "examples", "std-"+name, "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("std/%s example: %+v %v", name, ds, err)
	}
	source, err := os.ReadFile(filepath.Join(repository, "testdata", "std", name+"-inline.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	inline, ds := notation.Parse(source)
	if inline == nil || len(ds) != 0 {
		t.Fatalf("std/%s inline: %+v", name, ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("std/%s compile: %+v", name, ds)
	}
	return score, inline, p
}
