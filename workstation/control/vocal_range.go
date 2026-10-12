package control

import "math"

type VocalRange struct{ Start, End int64 }

func vocalSnap(tick, grid int64) int64 {
	if grid <= 1 {
		return tick
	}
	return int64(math.Round(float64(tick)/float64(grid))) * grid
}

// SelectVocalRange accepts either drag direction and keeps clicks nonempty.
// Covered take boundaries remain selectable even when they are off the grid.
func SelectVocalRange(anchor, head, lo, hi, grid int64) VocalRange {
	a := min(hi, max(lo, vocalSnap(anchor, grid)))
	b := min(hi, max(lo, vocalSnap(head, grid)))
	if a > b {
		a, b = b, a
	}
	if a == b {
		b = min(hi, a+max(int64(1), grid))
		if a == b {
			a = max(lo, b-max(int64(1), grid))
		}
	}
	return VocalRange{a, b}
}

func TrimVocalRange(r VocalRange, end bool, tick, lo, hi, grid int64) VocalRange {
	tick = vocalSnap(tick, grid)
	if end {
		r.End = min(hi, max(r.Start+1, tick))
	} else {
		r.Start = max(lo, min(r.End-1, tick))
	}
	return r
}

func MoveVocalRange(r VocalRange, delta, lo, hi, grid int64) VocalRange {
	delta = min(hi-r.End, max(lo-r.Start, vocalSnap(delta, grid)))
	return VocalRange{r.Start + delta, r.End + delta}
}
