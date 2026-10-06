// cicada-convolution-wasm prepares a separately loaded IR outside the callback.
package main

import (
	"m31labs.dev/cicada/kernel/fx/convolution"
	"unsafe"
)

var impulseL, impulseR []float32
var reverb *convolution.Reverb
var pcm [256]float32

//go:wasmexport cicada_ir_alloc
func alloc(frames int32) uint32 {
	if reverb != nil || len(impulseL) != 0 || frames < 1 || frames > convolution.MaxFrames {
		return 0
	}
	impulseL = make([]float32, frames)
	impulseR = make([]float32, frames)
	return uint32(uintptr(unsafe.Pointer(&impulseL[0])))
}

//go:wasmexport cicada_ir_right_ptr
func rightPtr() uint32 {
	if len(impulseR) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&impulseR[0])))
}

//go:wasmexport cicada_ir_init
func initIR(rate, partition int32) int32 {
	if reverb != nil || len(impulseL) == 0 {
		return -1
	}
	var err error
	reverb, err = convolution.New(int(rate), impulseL, impulseR, int(partition))
	if err != nil {
		return -1
	}
	return 0
}

//go:wasmexport cicada_ir_pcm_ptr
func pcmPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&pcm[0]))) }

//go:wasmexport cicada_ir_process
func process(frames int32) int32 {
	if reverb == nil || frames < 1 || frames > 128 {
		return -1
	}
	for i := 0; i < int(frames); i++ {
		pcm[i], pcm[128+i] = reverb.Process(pcm[i], pcm[128+i])
	}
	if reverb.Fault() {
		return -1
	}
	return 0
}

//go:wasmexport cicada_ir_latency
func latency() int32 {
	if reverb == nil {
		return -1
	}
	return int32(reverb.LatencyFrames())
}

//go:wasmexport cicada_ir_reset
func reset() {
	if reverb != nil {
		reverb.Reset()
	}
}
func main() {}
