//go:build tinygo && cicada_reactor

package cmd

import "m31labs.dev/cicada/kernel"

// The reactor reports numeric command results; hosts keep full diagnostics.
func validationError(_ string) error { return kernel.ReactorValidationError }
