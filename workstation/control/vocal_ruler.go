package control

import "math"

const vocalRulerTicksPerBar = int64(3840)

// VocalRulerTicks returns a bounded set of bar labels for the visible waveform
// width. An unmeasured or collapsed surface waits for its next resize rather
// than choosing a spacing from zero or negative pixels. The tick limit matches
// the native vocal-comp range; no ruler needs more than 512 labels.
func VocalRulerTicks(end int64, width float64) []int64 {
	if end <= 0 || end > 1<<40 || width <= 0 || math.IsNaN(width) || math.IsInf(width, 0) {
		return nil
	}
	bars := max(1., float64(end)/float64(vocalRulerTicksPerBar))
	lastSpacing := (end-1)/vocalRulerTicksPerBar + 1
	spacing := int64(1)
	for width/bars*float64(spacing) < 60 || bars/float64(spacing) > 512 {
		if spacing >= lastSpacing {
			break
		}
		// Bound doubling by the song before multiplying into tick spacing.
		// A surface narrower than one label still shows its first bar.
		if spacing > lastSpacing/2 {
			spacing = lastSpacing
			break
		}
		spacing *= 2
	}
	step := vocalRulerTicksPerBar * spacing
	ticks := make([]int64, 0, min(int64(512), (end-1)/step+1))
	for tick := int64(0); tick < end && len(ticks) < 512; tick += step {
		ticks = append(ticks, tick)
	}
	return ticks
}
