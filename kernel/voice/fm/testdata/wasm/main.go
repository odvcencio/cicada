package main

import (
	"m31labs.dev/cicada/kernel/voice/fm"
	"m31labs.dev/cicada/kernel/voice/fm/testdata/fixture"
	"runtime"
	"unsafe"
)

var output [fixture.Frames * 2]float32
var instrument *fm.Instrument
var count int32

//go:wasmexport fm_fixture_render
func render(index, blockSize int32) int32 {
	if fixture.Render(int(index), int(blockSize), output[:]) != nil {
		return -1
	}
	return 0
}

//go:wasmexport fm_fixture_ptr
func pointer() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

//go:wasmexport fm_bench_init
func initBenchmark(voices int32) int32 {
	if voices < 0 || voices > fm.MaxVoices {
		return -1
	}
	var err error
	instrument, err = fm.New(48000, fm.DefaultParams())
	if err != nil {
		return -1
	}
	if voices > 0 {
		_ = instrument.SetVoiceLimit(int(voices))
	}
	count = voices
	strike()
	return 0
}

//go:wasmexport fm_bench_strike
func strike() {
	instrument.Reset()
	for i := int32(0); i < count; i++ {
		_ = instrument.NoteOn(uint8(48+i*3), 100)
	}
}

//go:wasmexport fm_bench_render
func benchmark(frames int32) {
	for i := int32(0); i < frames; i++ {
		output[i], output[fixture.Frames+i] = instrument.NextStereo()
	}
}

//go:wasmexport fm_bench_alloc
func allocationBytes() uint64 { var m runtime.MemStats; runtime.ReadMemStats(&m); return m.TotalAlloc }

func main() {}
