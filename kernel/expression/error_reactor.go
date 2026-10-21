//go:build tinygo && cicada_reactor

package expression

import "m31labs.dev/cicada/kernel"

// The audio reactor returns numeric initialization codes; native callers keep
// the full diagnostics. All validation branches remain in both builds.
func validationError(_ string) error { return kernel.ReactorValidationError }
