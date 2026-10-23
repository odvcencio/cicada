package musictime

import "testing"

func TestSelectionSecondsFollowTempoSteps(t *testing.T) {
	steps := []TempoChange{{Bar: 1, BPMMilli: 60000}, {Bar: 3, BPMMilli: 120000}}
	for _, test := range []struct {
		start, end int64
		seconds    float64
	}{{0, 4 * 3840, 12}, {3840, 4 * 3840, 10}, {2 * 3840, 3 * 3840, 4}, {3840 + 960, 2 * 3840, 3}} {
		if got := SecondsBetween(120000, steps, test.start, test.end); got != test.seconds {
			t.Fatal(test, got)
		}
	}
}
