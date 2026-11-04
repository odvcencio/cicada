//go:build tinygo || wasm

package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// The native Studio host owns MIDI pickup. Browser kernel builds do not pay
// for a native control observer in their realtime engine.
type parameterObserver struct{}

func (*Engine) SetParameterObserver(func(uint8, kernel.ParamID, float32)) {}
func (e *Engine) setParam(c cmd.Command)                                  { e.setParamMode(c, false) }
func (e *Engine) setParamImmediate(c cmd.Command)                         { e.setParamMode(c, true) }
