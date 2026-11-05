package patternmachine

import (
	"reflect"
	"testing"
)

func TestGenerateRepeatableAndScoped(t *testing.T) {
	r := Recipe{Version: 1, Seed: 34891, Bars: 4, Root: 6, Scale: "minor", Density: 85, Motion: 60}
	a, err := Generate(r, "notes", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(r, "notes", nil)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("same recipe changed")
	}
	r.Seed++
	c, err := Generate(r, "notes", nil)
	if err != nil || reflect.DeepEqual(a, c) {
		t.Fatal("different seed did not vary the pattern")
	}
	for i, n := range a.Notes {
		if n.Velocity > 0 && (n.Pitch < 12 || n.Pitch > 95) {
			t.Fatalf("step %d out of range: %+v", i, n)
		}
	}
	if len(a.Notes) != 64 || a.Notes[0].Pitch != 54 || a.Notes[48].Pitch != 61 {
		t.Fatal("missing F# minor and C# dominant anchors", a.Notes[0], a.Notes[48])
	}
	d, err := Generate(r, "drums", []string{"bd", "sd", "ch"})
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range []string{"bd", "sd", "ch"} {
		if len(d.Lanes[lane]) != 64 {
			t.Fatal("wrong lane length", lane)
		}
	}
	for bar := 0; bar < 4; bar++ {
		if d.Lanes["bd"][bar*16] == 0 || d.Lanes["sd"][bar*16+4] == 0 || d.Lanes["sd"][bar*16+12] == 0 {
			t.Fatal("lost the backbeat", bar)
		}
	}
	for _, scale := range []string{"major", "minor", "dorian", "phrygian", "harmonic", "mixo", "pent", "blues"} {
		r.Scale = scale
		if _, err := Generate(r, "notes", nil); err != nil {
			t.Fatal(scale, err)
		}
	}
}

func TestGenerateRejectsInvalidRecipe(t *testing.T) {
	good := Recipe{Version: 1, Bars: 4, Root: 6, Scale: "minor", Density: 80, Motion: 50}
	cases := []Recipe{good, good, good, good, good}
	cases[0].Version = 2
	cases[1].Bars = 3
	cases[2].Root = 12
	cases[3].Density = 101
	cases[4].Scale = "unknown"
	for _, r := range cases {
		if _, err := Generate(r, "notes", nil); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}
