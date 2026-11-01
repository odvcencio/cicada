package main

import (
	"testing"

	"m31labs.dev/cicada/project"
)

func TestScoreCountInLabel(t *testing.T) {
	if got := scoreCountInLabel(nil); got != "One-bar count-in" {
		t.Fatal(got)
	}
	for _, test := range []struct {
		bars  int
		label string
	}{{0, "No count-in"}, {1, "One-bar count-in"}, {2, "2-bar count-in"}, {8, "8-bar count-in"}} {
		p := &project.Project{Live: &project.Live{CountInBars: &test.bars}}
		if got := scoreCountInLabel(p); got != test.label {
			t.Fatalf("%d: %s", test.bars, got)
		}
	}
}
