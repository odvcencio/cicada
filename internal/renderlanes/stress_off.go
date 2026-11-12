//go:build !lanestress

package renderlanes

// StressEnabled reports whether this build has the lane stress hook
// (the lanestress build tag).
const StressEnabled = false

// StressPoint does nothing without the lanestress build tag.
func StressPoint() {}

// StressSeed is zero without the lanestress build tag.
func StressSeed() uint64 { return 0 }
