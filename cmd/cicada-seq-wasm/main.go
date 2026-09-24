// cicada-seq-wasm is a small WASM compatibility probe for the M0 sequencer.
// It is not yet the audio kernel entry point.
package main

import "m31labs.dev/cicada/kernel/seq"

//go:wasmexport cicada_seq_sample_at_tick
func sampleAtTick(sampleRate, bpmMilli, tick int64) int64 {
	c, err := seq.NewClock(int(sampleRate), bpmMilli)
	if err != nil {
		return -1
	}
	return c.SampleAtTick(tick)
}

//go:wasmexport cicada_seq_tick_at_sample
func tickAtSample(sampleRate, bpmMilli, sample int64) int64 {
	c, err := seq.NewClock(int(sampleRate), bpmMilli)
	if err != nil {
		return -1
	}
	return c.TickAtSample(sample)
}

func main() {}
