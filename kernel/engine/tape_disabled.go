//go:build tinygo

package engine

import "m31labs.dev/cicada/kernel/fx/tape"

// tapeReturn is inert in portable kernels: newTapeReturn always refuses, so
// the engine never holds one and none of these methods run.
type tapeReturn struct{}

func newTapeReturn(int, int64) (*tapeReturn, error) {
	return nil, engineError("the tape echo is not available in portable kernels")
}

func (*tapeReturn) Params() tape.Params                            { return tape.Params{} }
func (*tapeReturn) Fault() bool                                    { return false }
func (*tapeReturn) Silent() bool                                   { return true }
func (*tapeReturn) SetTempo(int64) error                           { return nil }
func (*tapeReturn) SetParams(tape.Params) error                    { return nil }
func (*tapeReturn) Reset()                                         {}
func (*tapeReturn) Process(left, right float32) (float32, float32) { return 0, 0 }

// tapeEnabled lets TinyGo drop every tape call site, not only the DSP.
const tapeEnabled = false
