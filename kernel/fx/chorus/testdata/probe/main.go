// The parity probe exports one isolated processor without linking the engine.
package main

import (
	"math"

	"m31labs.dev/cicada/kernel/fx/chorus"
)

var processor *chorus.Processor

//go:wasmexport probe_init
func initProbe(rate, voices uint32) uint32 {
	params := chorus.DefaultParams()
	params.Voices, params.Rate, params.Depth, params.Spread, params.Mix = int(voices), 1.7, 5, .4, .7
	var err error
	processor, err = chorus.New(int(rate), params)
	if err != nil {
		return 1
	}
	return 0
}

//go:wasmexport probe_step
func stepProbe(index uint32) uint64 {
	left := float32(int(index%511)-255) / 1024
	right := float32(int((index*37)%503)-251) / 1024
	l, r := processor.Process(left, right)
	return uint64(math.Float32bits(l)) | uint64(math.Float32bits(r))<<32
}

func main() {}
