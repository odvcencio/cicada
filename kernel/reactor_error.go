//go:build tinygo && cicada_reactor

package kernel

// Reactors expose numeric status codes. Share one immutable error value across
// DSP packages so each package does not retain its own interface metadata.
// Native and other TinyGo callers keep their full, typed diagnostics.
type reactorValidationError uint8

func (reactorValidationError) Error() string { return "invalid parameters" }

const ReactorValidationError = reactorValidationError(0)
