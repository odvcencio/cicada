package phrase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestSeedFixturesAndReferenceTrace(t *testing.T) {
	for seed := uint64(0); seed < 24; seed++ {
		params := DefaultParams()
		params.Seed, params.Key, params.Scale = seed, 9, Minor
		result, err := Generate(params)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		path := filepath.Join("..", "examples", "gen", fmt.Sprintf("seed-%04d.cicada", seed))
		fixture, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal([]byte(result.Notation), fixture) {
			t.Fatalf("seed %d changed from its checked-in source fixture", seed)
		}
	}
	params := DefaultParams()
	params.Seed, params.Key, params.Scale = 4242, 9, Minor
	result, err := Generate(params)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "examples", "gen", "seed-4242.trace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var trace []Draw
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatal(err)
	}
	if len(trace) < 8 || !reflect.DeepEqual(trace, result.Trace) {
		t.Fatal("seed 4242 draw trace changed")
	}
	wantRaw := [...]uint32{744572222, 3209802928, 3019347011, 1444383813, 3602554767, 1236057910, 546096739, 2432106775}
	for index, raw := range wantRaw {
		if trace[index].Raw != raw {
			t.Fatalf("PCG draw %d: got %d want %d", index, trace[index].Raw, raw)
		}
	}
}

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
	rootOnsets, totalOnsets := 0, 0
	for scale := Minor; scale <= Blues; scale++ {
		for seed := uint64(0); seed < 10_000; seed++ {
			params := DefaultParams()
			params.Scale, params.Seed = scale, seed
			p, err := normalize(params)
			if err != nil {
				t.Fatal(err)
			}
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
				bar := result.Bars[index]
				activeCells, accents, rootSeen := 0, 0, false
				previousPitch := -1
				for stepIndex := uint8(0); stepIndex < bar.Len; stepIndex++ {
					step, err := seq.UnpackStep(bar.Steps[stepIndex])
					if err != nil {
						t.Fatalf("bar %d step %d: %v", index, stepIndex, err)
					}
					if step.Slide {
						next, err := seq.UnpackStep(bar.Steps[(stepIndex+1)%bar.Len])
						if err != nil || !next.Gate {
							t.Fatalf("slide into rest: scale=%d seed=%d bar=%d step=%d", scale, seed, index, stepIndex)
						}
					}
					if !step.Gate {
						continue
					}
					activeCells++
					if step.Tie {
						continue
					}
					if step.Accent {
						accents++
					}
					if step.Note%12 == params.Key {
						rootSeen = true
					}
					if previousPitch >= 0 && abs(int(step.Note)-previousPitch) > 12 {
						t.Fatalf("wide interval: scale=%d seed=%d bar=%d step=%d", scale, seed, index, stepIndex)
					}
					previousPitch = int(step.Note)
				}
				if delta := activeCells - onsetTarget(int(p.Steps), int(p.density)); delta < -1 || delta > 1 || !rootSeen {
					t.Fatalf("distribution gate: scale=%d seed=%d bar=%d active cells=%d root=%t", scale, seed, index, activeCells, rootSeen)
				}
				if accents < 2 || accents > 6 {
					t.Fatalf("accent distribution: scale=%d seed=%d bar=%d accents=%d", scale, seed, index, accents)
				}
			}
			base, _, err := buildBaseBar(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, note := range base {
				if note.active && !note.tie {
					totalOnsets++
					if note.class == classRoot {
						rootOnsets++
					}
				}
			}
		}
	}
	rootShare := float64(rootOnsets) / float64(totalOnsets)
	if rootShare < .30 || rootShare > .45 {
		t.Fatalf("root class share %.3f outside 30–45%% over %d onsets", rootShare, totalOnsets)
	}
	t.Logf("root-class share: %.3f; all 320000 bars have 2–6 accents", rootShare)
}

func TestDefaultStructureAccentRepairPreservesDrawTrace(t *testing.T) {
	params := DefaultParams()
	params.Seed, params.Scale = 97, Minor
	p, err := normalize(params)
	if err != nil {
		t.Fatal(err)
	}
	base, baseTrace, err := buildBaseBar(p)
	if err != nil {
		t.Fatal(err)
	}
	rawB, mutateTrace, err := mutateNotes(base, p.Seed^0xB, []Op{{Kind: NudgeDegree}, {Kind: ToggleAccent}, {Kind: ToggleSlide}}, 0, p)
	if err != nil {
		t.Fatal(err)
	}
	rawAccents := 0
	for _, note := range rawB {
		if note.active && !note.tie && note.accent {
			rawAccents++
		}
	}
	if rawAccents != 1 {
		t.Fatalf("seed 97 raw B accents = %d, want 1", rawAccents)
	}
	result, err := Generate(params)
	if err != nil {
		t.Fatal(err)
	}
	wantTrace := append(baseTrace, mutateTrace...)
	if !reflect.DeepEqual(result.Trace, wantTrace) {
		t.Fatal("accent repair changed the seeded draw trace")
	}
	accents := 0
	for stepIndex := uint8(0); stepIndex < result.Bars[2].Len; stepIndex++ {
		step, err := seq.UnpackStep(result.Bars[2].Steps[stepIndex])
		if err != nil {
			t.Fatal(err)
		}
		if step.Gate && !step.Tie && step.Accent {
			accents++
		}
	}
	if accents != 2 {
		t.Fatalf("seed 97 repaired B accents = %d, want 2", accents)
	}
}
