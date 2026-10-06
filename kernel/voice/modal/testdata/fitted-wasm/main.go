package main

import (
	"m31labs.dev/cicada/kernel/voice/modal"
	"unsafe"
)

var parameters [1 + modal.MaxModes*3]float64
var output [128]float32
var voice *modal.FittedVoice

func main() {}

//go:wasmexport parameters_ptr
func parametersPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&parameters[0]))) }

//go:wasmexport output_ptr
func outputPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }

//go:wasmexport prepare
func prepare(count uint32) uint32 {
	if count > modal.MaxModes {
		return 1
	}
	m := modal.Model{Count: int(count), NoiseMix: parameters[0]}
	for i := 0; i < int(count); i++ {
		m.Modes[i] = modal.Mode{Ratio: parameters[1+i*3], Weight: parameters[2+i*3], T60: parameters[3+i*3]}
	}
	var err error
	voice, err = modal.NewFittedVoice(m, 48000)
	if err != nil {
		return 1
	}
	return 0
}

//go:wasmexport note_on
func noteOn(note, velocity, slide uint32) { voice.NoteOn(uint8(note), uint8(velocity), slide != 0) }

//go:wasmexport render
func render() {
	for i := range output {
		output[i] = voice.Next()
	}
}
