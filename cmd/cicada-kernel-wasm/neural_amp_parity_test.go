//go:build wasm_integration

package main

import "testing"

func TestAudioWASMNeuralAmpSampleParity(t *testing.T) {
	compareWASMFixture(t, "neural-amp.cicada", 1)
}
