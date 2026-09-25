package phrase

import (
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGeneratedBaseBarParsesAndCompilesToSameSteps(t *testing.T) {
	for scale := Minor; scale <= Blues; scale++ {
		for seed := uint64(0); seed < 20; seed++ {
			params := DefaultParams()
			params.Seed, params.Key, params.Scale = seed, 9, scale
			p, err := normalize(params)
			if err != nil {
				t.Fatal(err)
			}
			notes, trace, err := buildBaseBar(p)
			if err != nil {
				t.Fatalf("scale=%d seed=%d: %v", scale, seed, err)
			}
			if len(trace) == 0 {
				t.Fatal("generated bar has no draw trace")
			}
			want, err := encodeBar(notes, p)
			if err != nil {
				t.Fatal(err)
			}
			source, err := sourceForBar(notes, p)
			if err != nil {
				t.Fatal(err)
			}
			score, diagnostics := notation.Parse([]byte(source))
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == "error" {
					t.Fatalf("source parse scale=%d seed=%d: %+v\n%s", scale, seed, diagnostic, source)
				}
			}
			if compiled, diagnostics := project.FromScore(score); compiled == nil {
				t.Fatalf("project validation scale=%d seed=%d: %+v\n%s", scale, seed, diagnostics, source)
			}
			patterns, err := project.CompilePattern(score, score.Patterns[0], score.Tracks[0])
			if err != nil {
				t.Fatalf("pattern compile scale=%d seed=%d: %v\n%s", scale, seed, err, source)
			}
			if !reflect.DeepEqual(patterns[0].Pattern, want) {
				t.Fatalf("generated source changed steps: scale=%d seed=%d\n%s", scale, seed, source)
			}
		}
	}
}

func TestBaseBarSeedPopulation(t *testing.T) {
	for scale := Minor; scale <= Blues; scale++ {
		roots, soundingOnsets := 0, 0
		for seed := uint64(0); seed < 10_000; seed++ {
			params := DefaultParams()
			params.Seed, params.Scale = seed, scale
			p, err := normalize(params)
			if err != nil {
				t.Fatal(err)
			}
			notes, _, err := buildBaseBar(p)
			if err != nil {
				t.Fatalf("scale=%d seed=%d: %v", scale, seed, err)
			}
			barOnsets, accents := 0, 0
			for index, note := range notes {
				if !note.active {
					continue
				}
				barOnsets++
				if note.accent {
					accents++
				}
				if note.tie {
					continue
				}
				soundingOnsets++
				if note.class == classRoot {
					roots++
				}
				previous, _ := adjacentOnsets(notes, index)
				if previous >= 0 && abs(note.note-notes[previous].note) > 12 {
					t.Fatalf("interval exceeds 12: scale=%d seed=%d step=%d", scale, seed, index)
				}
			}
			if !notes[0].active || notes[0].class != classRoot || barOnsets < onsetTarget(16, int(p.density))-1 || barOnsets > onsetTarget(16, int(p.density))+1 || accents < 2 || accents > 6 {
				t.Fatalf("bar gate: scale=%d seed=%d onsets=%d accents=%d", scale, seed, barOnsets, accents)
			}
		}
		if percent := roots * 100 / soundingOnsets; percent < 30 || percent > 45 {
			t.Fatalf("root share for scale %d is %d%%", scale, percent)
		}
	}
}

func TestBaseBarSourceVariants(t *testing.T) {
	for _, tc := range []struct {
		key, steps uint8
		scale      Scale
		rest       bool
		seed       uint64
	}{
		{0, 8, Minor, false, 1},
		{1, 16, Blues, true, 1<<40 + 4242},
		{6, 32, Major, false, 99},
		{11, 64, HarmonicMinor, true, 100},
	} {
		params := DefaultParams()
		params.Key, params.Steps, params.Scale, params.RestDownbeat, params.Seed = tc.key, tc.steps, tc.scale, tc.rest, tc.seed
		p, err := normalize(params)
		if err != nil {
			t.Fatal(err)
		}
		notes, _, err := buildBaseBar(p)
		if err != nil {
			t.Fatal(err)
		}
		want, err := encodeBar(notes, p)
		if err != nil {
			t.Fatal(err)
		}
		source, err := sourceForBar(notes, p)
		if err != nil {
			t.Fatal(err)
		}
		score, diagnostics := notation.Parse([]byte(source))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("source parse: %+v\n%s", diagnostic, source)
			}
		}
		patterns, err := project.CompilePattern(score, score.Patterns[0], score.Tracks[0])
		if err != nil || !reflect.DeepEqual(patterns[0].Pattern, want) {
			t.Fatalf("source parity: %v\n%s", err, source)
		}
	}
}
