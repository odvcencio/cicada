package main

import (
	"m31labs.dev/cicada/kernel/voice/strings"
	"m31labs.dev/cicada/kernel/voice/strings/testdata/fixture"
	"runtime"
	"unsafe"
)

var output [fixture.Frames * 2]float32
var instrument *strings.Instrument

//go:wasmexport keys_fixture_render
func render(index, blockSize int32) int32 {
	if fixture.Render(int(index), int(blockSize), output[:]) != nil {
		return -1
	}
	return 0
}

//go:wasmexport keys_fixture_ptr
func pointer() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

//go:wasmexport keys_alloc_init
func initAllocation() { instrument, _ = strings.New(48000, strings.DefaultParams()) }

//go:wasmexport keys_alloc_render
func renderAllocation() {
	_ = instrument.SetVoiceLimit(3)
	for n := uint8(48); n < 60; n++ {
		_ = instrument.NoteOn(n, 100)
	}
	_ = instrument.SetSustain(1)
	instrument.NoteOff(60)
	for n := 0; n < 128; n++ {
		output[n], output[fixture.Frames+n] = instrument.NextStereo()
	}
	instrument.AllNotesOff()
	_ = instrument.SetSustain(0)
	instrument.Reset()
}

//go:wasmexport keys_alloc_bytes
func allocationBytes() uint64 { var m runtime.MemStats; runtime.ReadMemStats(&m); return m.TotalAlloc }

func main() {}
