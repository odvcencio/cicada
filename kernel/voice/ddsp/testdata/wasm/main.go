package main

import (
	"m31labs.dev/cicada/kernel/voice/ddsp"
	"m31labs.dev/cicada/kernel/voice/ddsp/testdata/fixture"
	"unsafe"
)

var output [fixture.Frames]float32
var voices [32]ddsp.Synth

//go:wasmexport ddsp_fixture_render
func render(index, blockSize int32) int32 {
	if !fixture.Render(int(index), int(blockSize), output[:]) {
		return -1
	}
	return 0
}

//go:wasmexport ddsp_fixture_ptr
func pointer() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

//go:wasmexport ddsp_perf_init
func perfInit() {
	for i := range voices {
		voices[i], _ = ddsp.New(48000)
	}
}

//go:wasmexport ddsp_perf_block
func perfBlock(count int32) int32 {
	if count < 1 || count > 32 {
		return -1
	}
	var sum int32
	for frame := 0; frame < 128; frame++ {
		for i := 0; i < int(count); i++ {
			sum += int32(voices[i].Next(110000+uint32(i)*17000, 26000))
		}
	}
	return sum
}

func main() {}
