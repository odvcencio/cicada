package main

import (
	"m31labs.dev/cicada/kernel/stream/testdata/fixture"
	"unsafe"
)

var output [fixture.Frames * 2]float32

//go:wasmexport stream_fixture_render
func render(size int32) int32 {
	if err := fixture.Render(int(size), output[:]); err != nil {
		return -1
	}
	return 0
}

//go:wasmexport stream_fixture_ptr
func pointer() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

func main() {}
