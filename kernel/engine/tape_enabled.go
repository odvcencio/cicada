//go:build !tinygo

package engine

import "m31labs.dev/cicada/kernel/fx/tape"

// tapeReturn is the tape echo delay return. Portable (TinyGo) kernels use an
// inert stub instead, so the tape DSP stays out of the size-budgeted core;
// portable images refuse a tape return before the engine is built.
type tapeReturn = tape.Tape

func newTapeReturn(sampleRate int, bpmMilli int64) (*tapeReturn, error) {
	return tape.New(sampleRate, bpmMilli)
}

// tapeEnabled lets TinyGo drop every tape call site, not only the DSP.
const tapeEnabled = true
