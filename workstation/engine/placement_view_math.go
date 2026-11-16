package main

import (
	"math"
	"strconv"
)

// placementPlayheadFraction is the playhead's place along the lanes: its tick
// over the length the lanes span, both in the same ticks. The transport's
// position is in real song ticks (a 3/4 bar is 2880), so a scene song passes
// its real length and the placement editor the length of its 4/4 grid.
func placementPlayheadFraction(tick, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, tick/total))
}

func nextPlacementZoom(current string, fit float64, in bool) string {
	levels := []struct {
		name  string
		width float64
	}{{"fit", fit}, {"48", 48}, {"96", 96}, {"192", 192}, {"384", 384}}
	width := fit
	if current != "fit" {
		width, _ = strconv.ParseFloat(current, 64)
	}
	best, distance := current, math.Inf(1)
	for _, level := range levels {
		diff := level.width - width
		if (in && diff > 0.000001 || !in && diff < -0.000001) && math.Abs(diff) < distance {
			best, distance = level.name, math.Abs(diff)
		}
	}
	return best
}
