// cicada-mix-wasm is an optional mix kernel, loaded separately from the core.
package main

import (
	"m31labs.dev/cicada/kernel/fx/pro"
	"unsafe"
)

var chain *pro.Chain
var pcm [256]float32

//go:wasmexport cicada_mix_init
func initMix(rate, preset int32) int32 {
	if chain != nil || preset < 0 || int(preset) >= len(pro.PresetNames) {
		return -1
	}
	params, ok := pro.Preset(pro.PresetNames[preset])
	if !ok {
		return -1
	}
	var err error
	chain, err = pro.New(int(rate), params)
	if err != nil {
		return -1
	}
	return 0
}

//go:wasmexport cicada_mix_pcm_ptr
func pcmPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&pcm[0]))) }

//go:wasmexport cicada_mix_process
func process(frames int32) int32 {
	if chain == nil || frames < 1 || frames > 128 {
		return -1
	}
	for i := 0; i < int(frames); i++ {
		pcm[i], pcm[128+i] = chain.Process(pcm[i], pcm[128+i])
	}
	if chain.Fault() {
		return -1
	}
	return 0
}

//go:wasmexport cicada_mix_latency
func latency() int32 {
	if chain == nil {
		return -1
	}
	return int32(chain.LatencyFrames())
}

//go:wasmexport cicada_mix_reset
func reset() {
	if chain != nil {
		chain.Reset()
	}
}
func main() {}
