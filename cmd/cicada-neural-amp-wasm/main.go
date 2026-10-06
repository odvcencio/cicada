// cicada-neural-amp-wasm probes the same integer amp linked into the kernel.
package main

import (
	"m31labs.dev/cicada/kernel/amp"
	"unsafe"
)

var model amp.Model
var pcm [128]int32

//go:wasmexport cicada_amp_pcm_ptr
func pcmPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&pcm[0]))) }

//go:wasmexport cicada_amp_process
func process(frames, drive int32) int32 {
	if frames < 1 || frames > 128 {
		return -1
	}
	for i := 0; i < int(frames); i++ {
		pcm[i] = model.Process(pcm[i], drive)
	}
	return 0
}

//go:wasmexport cicada_amp_reset
func reset() { model.Reset() }

func main() {}
