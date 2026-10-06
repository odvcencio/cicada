//go:build wasm_integration

package main

import "testing"

func TestAudioWASMTupletGridSampleParity(t *testing.T) {
	compareWASMSource(t, "tuplet-grid", []byte("cicada 2 track bass acid {} pattern triplet { step = 1/8t 1 3 5 } scene main { bass = triplet } song { main*2 }"), 2, true)
}
