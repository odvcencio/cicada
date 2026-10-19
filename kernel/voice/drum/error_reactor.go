//go:build tinygo && cicada_reactor

package drum

// The audio reactor returns numeric initialization codes; native callers keep
// the full diagnostics. All validation branches remain in both builds.
func validationError(_ string) error { return Error("invalid parameters") }
