// cicada-neural-amp-wasm probes the same integer amp linked into the kernel.
package main

import (
	"m31labs.dev/cicada/kernel/amp"
	"unsafe"
)

var model amp.Model
var ensemble [8]amp.Model
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

//go:wasmexport cicada_amp_process_eight
func processEight(frames, drive int32) int32 {
	if frames < 1 || frames > 128 {
		return -1
	}
	if drive < 0 {
		drive = 0
	}
	if drive > 32768 {
		drive = 32768
	}
	for i := 0; i < int(frames); i++ {
		input := pcm[i]
		var output int32
		for voice := range ensemble {
			output += ensemble[voice].Process(input, drive+int32(voice)*512)
		}
		pcm[i] = output >> 3
	}
	return 0
}

//go:wasmexport cicada_amp_reset
func reset() {
	model.Reset()
	ensemble = [8]amp.Model{}
}

func main() {}
