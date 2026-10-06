// Package studiolibtest loads the Studio library example and its inline score.
package studiolibtest

import (
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"os"
	"path/filepath"
	"testing"
)

func Load(t testing.TB, repository string) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	score, ds, err := project.LoadScore(filepath.Join(repository, "examples", "studio-library", "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("studio library: %v %+v", err, ds)
	}
	data, err := os.ReadFile(filepath.Join(repository, "testdata", "studio-library-inline.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	inline, ds := notation.Parse(data)
	if inline == nil || len(ds) != 0 {
		t.Fatalf("inline: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("project: %+v", ds)
	}
	return score, inline, p
}
