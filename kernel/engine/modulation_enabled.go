//go:build !tinygo || modular

package engine

// modulationEnabled compiles the modulator evaluator in. The core browser
// kernel leaves it out; a modular build (tag modular) and every native build
// keep it (spec 24, option A).
const modulationEnabled = true
