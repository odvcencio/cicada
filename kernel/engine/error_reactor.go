//go:build tinygo && cicada_reactor

package engine

import "m31labs.dev/cicada/kernel"

// The reactor exposes numeric initialization/fault codes. Native callers and
// other TinyGo consumers retain the descriptive errors in error_native.go.
func engineError(_ string) error { return kernel.ReactorValidationError }
