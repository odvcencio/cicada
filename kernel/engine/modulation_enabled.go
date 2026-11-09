//go:build !tinygo || modular

package engine

// modulationEnabled compiles the modulator evaluator in. The core browser
// kernel leaves it out; a modular build (tag modular) and every native build
// keep it (spec 24, option A).
const modulationEnabled = true

// liveParamSlots sizes each voice's live instrument parameter slots
// (instrument.p1 to instrument.p8).
const liveParamSlots = 8
