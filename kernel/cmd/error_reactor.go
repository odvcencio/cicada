//go:build tinygo && cicada_reactor

package cmd

// The reactor reports numeric command results; hosts keep full diagnostics.
func validationError(_ string) error { return Error("invalid parameters") }
