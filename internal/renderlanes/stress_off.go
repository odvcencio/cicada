//go:build !lanestress

package renderlanes

// StressEnabled reports whether this build has the lane stress hook
// (the lanestress build tag).
const StressEnabled = false

// StressPoint does nothing without the lanestress build tag; every call
// inlines to nothing.
func StressPoint(site int, a, b int64) {}

// StressSeed is zero without the lanestress build tag.
func StressSeed() uint64 { return 0 }
