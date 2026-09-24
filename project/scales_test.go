package project

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func TestAllV1ScaleDegreesRenderAndRoundTrip(t *testing.T) {
	cases := []struct {
		scale   string
		degrees string
		want    []uint8
	}{
		{"minor", "1 2 3 4 5 6 7", []uint8{45, 47, 48, 50, 52, 53, 55}},
		{"major", "1 2 3 4 5 6 7", []uint8{45, 47, 49, 50, 52, 54, 56}},
		{"dorian", "1 2 3 4 5 6 7", []uint8{45, 47, 48, 50, 52, 54, 55}},
		{"phrygian", "1 2 3 4 5 6 7", []uint8{45, 46, 48, 50, 52, 53, 55}},
		{"harmonic", "1 2 3 4 5 6 7", []uint8{45, 47, 48, 50, 52, 53, 56}},
		{"mixo", "1 2 3 4 5 6 7", []uint8{45, 47, 49, 50, 52, 54, 55}},
		{"pent", "1 3 4 5 7", []uint8{45, 48, 50, 52, 55}},
		{"blues", "1 3 4 4# 5 7", []uint8{45, 48, 50, 51, 52, 55}},
	}
	for _, tc := range cases {
		t.Run(tc.scale, func(t *testing.T) {
			source := fmt.Sprintf("cicada 1\nkey a %s\ntrack bass acid {}\npattern p acid steps=%d { %s }\nscene s { bass=p }\nsong { s }\n", tc.scale, len(tc.want), tc.degrees)
			score, diagnostics := notation.Parse([]byte(source))
			if len(diagnostics) != 0 {
				t.Fatalf("parse: %+v", diagnostics)
			}
			p, diagnostics := FromScore(score)
			if p == nil || len(diagnostics) != 0 {
				t.Fatalf("project: %+v", diagnostics)
			}
			cfg, err := CompileEngine(p, 48_000, 128)
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range tc.want {
				step, err := seq.UnpackStep(cfg.Patterns[0].Slots[0].Steps[i])
				if err != nil || step.Note != want {
					t.Fatalf("degree %d = %d, %v; want %d", i, step.Note, err, want)
				}
			}
			normalized, err := ToSource(p)
			if err != nil {
				t.Fatal(err)
			}
			converted, diagnostics := notation.Parse(normalized)
			if len(diagnostics) != 0 {
				t.Fatalf("normalized source: %+v", diagnostics)
			}
			again, diagnostics := FromScore(converted)
			if again == nil || len(diagnostics) != 0 || !reflect.DeepEqual(p, again) {
				t.Fatalf("semantic round-trip changed %s: %+v", tc.scale, diagnostics)
			}
		})
	}
}

func TestPentAndBluesRejectMissingDegrees(t *testing.T) {
	for _, scale := range []string{"pent", "blues"} {
		for _, degree := range []string{"2", "6"} {
			t.Run(scale+"/"+degree, func(t *testing.T) {
				src := fmt.Sprintf("cicada 1\nkey a %s\ntrack bass acid {}\npattern p acid steps=1 { %s }\nscene s { bass=p }\nsong { s }\n", scale, degree)
				_, diagnostics := notation.Parse([]byte(src))
				for _, d := range diagnostics {
					if d.Code == "CICADA-SCALE-DEGREE" && d.Position.Line == 4 {
						return
					}
				}
				t.Fatalf("degree %s was accepted in %s: %+v", degree, scale, diagnostics)
			})
		}
	}
}

func TestSixteenTrackProjectCompiles(t *testing.T) {
	var source strings.Builder
	source.WriteString("cicada 1\n")
	for track := 0; track < 16; track++ {
		fmt.Fprintf(&source, "track t%d acid {}\n", track)
	}
	source.WriteString("pattern p acid steps=1 { 1 }\nscene s {")
	for track := 0; track < 16; track++ {
		fmt.Fprintf(&source, " t%d=p", track)
	}
	source.WriteString(" }\nsong { s }\n")
	score, diagnostics := notation.Parse([]byte(source.String()))
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("project: %+v", diagnostics)
	}
	if len(p.Tracks) != 16 {
		t.Fatalf("tracks = %d, want 16", len(p.Tracks))
	}
	if _, err := CompileEngine(p, 48_000, 128); err != nil {
		t.Fatal(err)
	}
}

func TestTrackAndPatternLengthBoundaries(t *testing.T) {
	for _, steps := range []int{64, 65} {
		source := fmt.Sprintf("cicada 1\ntrack bass acid {}\npattern p acid steps=%d { %s }\nscene s { bass=p }\nsong { s }\n", steps, strings.TrimSpace(strings.Repeat("1 ", steps)))
		score, diagnostics := notation.Parse([]byte(source))
		if steps == 65 {
			found := false
			for _, d := range diagnostics {
				if d.Code == "CICADA-STEPS" || d.Code == "CICADA-EXPANSION" {
					found = true
				}
			}
			if !found {
				t.Fatalf("65-step pattern accepted: %+v", diagnostics)
			}
			continue
		}
		if len(diagnostics) != 0 {
			t.Fatalf("64-step parse: %+v", diagnostics)
		}
		p, diagnostics := FromScore(score)
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("64-step project: %+v", diagnostics)
		}
		cfg, err := CompileEngine(p, 48_000, 128)
		if err != nil || cfg.Patterns[0].Slots[0].Len != 64 {
			t.Fatalf("64-step engine: len=%d err=%v", cfg.Patterns[0].Slots[0].Len, err)
		}
	}

	var source strings.Builder
	source.WriteString("cicada 1\n")
	for track := 0; track < 17; track++ {
		fmt.Fprintf(&source, "track t%d acid {}\n", track)
	}
	source.WriteString("pattern p acid steps=1 { 1 }\nscene s { t0=p }\nsong { s }\n")
	_, diagnostics := notation.Parse([]byte(source.String()))
	for _, d := range diagnostics {
		if d.Code == "CICADA-TRACKS" {
			return
		}
	}
	t.Fatalf("17-track score accepted: %+v", diagnostics)
}
