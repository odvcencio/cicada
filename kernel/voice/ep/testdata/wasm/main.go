package main

import (
	"m31labs.dev/cicada/kernel/voice/ep"
	"m31labs.dev/cicada/kernel/voice/ep/testdata/fixture"
	"runtime"
	"unsafe"
)

var output [fixture.Frames * 2]float32
var inst *ep.Instrument

func main() {}

//go:wasmexport ep_fixture_ptr
func ptr() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

//go:wasmexport ep_fixture_render
func render(index, block int32) int32 {
	if fixture.Render(int(index), int(block), output[:]) != nil {
		return -1
	}
	return 0
}

//go:wasmexport ep_bench_init
func initBench(voices int32) { p := ep.DefaultParams(); inst, _ = ep.New(48000, p) }

//go:wasmexport ep_bench_strike
func strike() {
	for n := 0; n < 8; n++ {
		_ = inst.NoteOn(uint8(36+n*5), 100)
	}
}

//go:wasmexport ep_bench_render
func bench(frames int32) {
	for n := int32(0); n < frames; n++ {
		inst.NextStereo()
	}
}

//go:wasmexport ep_bench_alloc
func alloc() uint64 { var s runtime.MemStats; runtime.ReadMemStats(&s); return s.TotalAlloc }
