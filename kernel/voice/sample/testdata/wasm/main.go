package main

import (
	"unsafe"

	"m31labs.dev/cicada/kernel/voice/sample/testdata/fixture"
)

var output [fixture.Frames * 2]float32

//go:wasmexport sample_fixture_render
func render(index, blockSize int32) int32 {
	if err := fixture.Render(int(index), int(blockSize), output[:]); err != nil {
		return -1
	}
	return 0
}

//go:wasmexport sample_fixture_ptr
func pointer() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

func main() {}
