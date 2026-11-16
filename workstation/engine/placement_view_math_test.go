package main

import "testing"

func TestPlacementPlayheadFraction(t *testing.T) {
	// Bar 2 of a five-bar 3/4 scene song: 2880 of 14400 ticks.
	if got := placementPlayheadFraction(2880, 5*2880); got != .2 {
		t.Fatalf("3/4 scene song: got %v, want .2", got)
	}
	// Bar 2 of four 4/4 bars in the placement editor.
	if got := placementPlayheadFraction(3840, 4*3840); got != .25 {
		t.Fatalf("placement grid: got %v, want .25", got)
	}
	if got := placementPlayheadFraction(100, 0); got != 0 {
		t.Fatalf("no length: got %v", got)
	}
	if got := placementPlayheadFraction(9000, 3840); got != 1 {
		t.Fatalf("past the end: got %v", got)
	}
}

func TestNextPlacementZoomOrdersFitByWidth(t *testing.T) {
	for _, tc := range []struct {
		current string
		fit     float64
		in      bool
		want    string
	}{
		{"fit", 150, true, "192"}, {"fit", 150, false, "96"}, {"96", 150, true, "fit"}, {"48", 30, false, "fit"},
	} {
		if got := nextPlacementZoom(tc.current, tc.fit, tc.in); got != tc.want {
			t.Errorf("%+v: got %s", tc, got)
		}
	}
}
