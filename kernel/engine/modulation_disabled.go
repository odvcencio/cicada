//go:build tinygo && !modular

package engine

const modulationEnabled = false

// liveParamSlots is zero in the core kernel, which has no live instrument
// parameters, so the voice keeps its size.
const liveParamSlots = 0
