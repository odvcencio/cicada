package render

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func presetsExample(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	score, ds, err := project.LoadScore(filepath.Join("..", "examples", "presets", "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("preset example: %+v %v", ds, err)
	}
	source, err := os.ReadFile(filepath.Join("..", "testdata", "presets-inline.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	inline, ds := notation.Parse(source)
	if inline == nil || len(ds) != 0 {
		t.Fatalf("inline: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("compile: %+v", ds)
	}
	return score, inline, p
}
func TestPresetsNativeAndOfflineByteParity(t *testing.T) {
	verifySourceNativeAndOfflineByteParity(t, presetsExample, "presets")
}
func TestPresetsRenderAllocationFree(t *testing.T) {
	verifySourceRenderAllocationFree(t, presetsExample, "presets")
}
func BenchmarkPresetsVoiceNext(b *testing.B) { benchmarkSourceVoiceNext(b, presetsExample) }
