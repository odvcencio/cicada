package phrase

import (
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGenerateStructuresCompileBackToSameBars(t *testing.T) {
	for _, structure := range []Structure{A, AABA, ABAB, ABAC, AAAB} {
		for scale := Minor; scale <= Blues; scale++ {
			for seed := uint64(0); seed < 20; seed++ {
				params := DefaultParams()
				params.Structure, params.Scale, params.Seed = structure, scale, seed
				result, err := Generate(params)
				if err != nil {
					t.Fatalf("structure=%d scale=%d seed=%d: %v", structure, scale, seed, err)
				}
				score, diagnostics := notation.Parse([]byte(result.Notation))
				for _, diagnostic := range diagnostics {
					if diagnostic.Severity == "error" {
						t.Fatalf("generated source invalid: structure=%d scale=%d seed=%d: %+v\n%s", structure, scale, seed, diagnostic, result.Notation)
					}
				}
				if compiled, diagnostics := project.FromScore(score); compiled == nil {
					t.Fatalf("generated project invalid: structure=%d scale=%d seed=%d: %+v\n%s", structure, scale, seed, diagnostics, result.Notation)
				}
				if len(score.Song) != len(result.Bars) {
					t.Fatalf("song has %d entries for %d bars", len(score.Song), len(result.Bars))
				}
				for index, entry := range score.Song {
					actual, err := compiledScenePattern(score, entry.Scene)
					if err != nil || !reflect.DeepEqual(actual, result.Bars[index]) {
						t.Fatalf("bar %d parity: structure=%d scale=%d seed=%d error=%v\n%s", index, structure, scale, seed, err, result.Notation)
					}
				}
				if structure == AABA {
					if !reflect.DeepEqual(result.Bars[0], result.Bars[1]) || !reflect.DeepEqual(result.Bars[0], result.Bars[3]) {
						t.Fatalf("repeated A changed: structure=%d scale=%d seed=%d", structure, scale, seed)
					}
				} else if structure != A && !reflect.DeepEqual(result.Bars[0], result.Bars[2]) {
					t.Fatalf("repeated A changed: structure=%d scale=%d seed=%d", structure, scale, seed)
				}
			}
		}
	}
}

func compiledScenePattern(score *notation.Score, sceneName string) (seq.Pattern, error) {
	for _, scene := range score.Scenes {
		if scene.Name != sceneName {
			continue
		}
		for _, binding := range scene.Bindings {
			if binding.Track != "bass" {
				continue
			}
			for _, pattern := range score.Patterns {
				if pattern.Name == binding.Pattern {
					compiled, err := project.CompilePattern(score, pattern, score.Tracks[0])
					if err != nil {
						return seq.Pattern{}, err
					}
					return compiled[0].Pattern, nil
				}
			}
		}
	}
	return seq.Pattern{}, seq.Error("missing generated scene pattern")
}

func TestGenerateDefaultSeedPopulation(t *testing.T) {
	for scale := Minor; scale <= Blues; scale++ {
		for seed := uint64(0); seed < 10_000; seed++ {
			params := DefaultParams()
			params.Scale, params.Seed = scale, seed
			result, err := Generate(params)
			if err != nil {
				t.Fatalf("scale=%d seed=%d: %v", scale, seed, err)
			}
			if len(result.Bars) != 4 || result.Notation == "" || len(result.Trace) == 0 {
				t.Fatalf("incomplete result: scale=%d seed=%d", scale, seed)
			}
			for index := range result.Bars {
				if err := result.Bars[index].Validate(); err != nil {
					t.Fatalf("invalid bar %d: scale=%d seed=%d: %v", index, scale, seed, err)
				}
			}
		}
	}
}
