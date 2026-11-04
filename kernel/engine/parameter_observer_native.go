//go:build !tinygo && !wasm

package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// A host installs the observer before publishing an engine to the render
// thread. It receives parameter targets, including automation and scene
// changes, without reading mutable DSP state from another goroutine.
type parameterObserver struct {
	onParameter func(uint8, kernel.ParamID, float32)
}

func (e *Engine) SetParameterObserver(fn func(uint8, kernel.ParamID, float32)) { e.onParameter = fn }

func (e *Engine) setParam(c cmd.Command) {
	e.setParamMode(c, false)
	if !e.faulted {
		e.observeParameter(c)
	}
}

func (e *Engine) setParamImmediate(c cmd.Command) {
	e.setParamMode(c, true)
	if !e.faulted {
		e.observeParameter(c)
	}
}

func (e *Engine) observeParameter(c cmd.Command) {
	if e.onParameter != nil {
		e.onParameter(c.Track, kernel.ParamID(c.Index), math.Float32frombits(c.Arg0))
	}
}
