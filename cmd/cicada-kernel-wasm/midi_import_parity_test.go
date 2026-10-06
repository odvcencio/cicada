//go:build wasm_integration

package main

import "testing"

func TestAudioWASMVelocityRowSampleParity(t *testing.T) {
	compareWASMSource(t, "import-velocity", []byte("cicada 2 track keys piano {} pattern triplet {step=1/8t c3 e3 g3 velocity: 64 100 127} scene main {keys=triplet} song {main}"), 1, true)
}
