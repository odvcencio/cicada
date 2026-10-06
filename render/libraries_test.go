package render

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func librariesExample(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	score, ds, err := project.LoadScore(filepath.Join("..", "examples", "libraries", "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("library example: %+v %v", ds, err)
	}
	source, err := os.ReadFile(filepath.Join("..", "testdata", "libraries-inlined.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	single, ds := notation.Parse(source)
	if single == nil || len(ds) != 0 {
		t.Fatalf("inlined example: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("compiled example: %+v", ds)
	}
	return score, single, p
}
func TestLibrariesNativeAndOfflineByteParity(t *testing.T) {
	verifySourceNativeAndOfflineByteParity(t, librariesExample, "libraries")
}
func TestLibrariesRenderAllocationFree(t *testing.T) {
	verifySourceRenderAllocationFree(t, librariesExample, "libraries")
}
func BenchmarkLibrariesVoiceNext(b *testing.B) { benchmarkSourceVoiceNext(b, librariesExample) }
