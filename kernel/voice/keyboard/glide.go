package keyboard

// Approach moves *cur one step of a one-pole glide toward target and reports
// whether it arrived. It snaps to the target when a step no longer changes the
// float32 value or lands within 1e-6, so every glide ends on the exact target
// and the caller can then stop running it.
func Approach(cur *float32, target, alpha float32) bool {
	next := *cur + alpha*(target-*cur)
	if next == *cur || next-target < 1e-6 && target-next < 1e-6 {
		*cur = target
		return true
	}
	*cur = next
	return false
}

// ValidGlide reports whether a glide coefficient is in (0, 1].
func ValidGlide(glide float32) bool { return glide > 0 && glide <= 1 }

// InRange reports whether v is a number within lo..hi; NaN is out of range.
func InRange(v, lo, hi float32) bool { return v >= lo && v <= hi }
