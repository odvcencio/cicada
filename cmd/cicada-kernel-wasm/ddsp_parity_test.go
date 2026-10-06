//go:build wasm_integration

package main

import "testing"

func TestAudioWASMDDSPScoreSampleParity(t *testing.T) {
	compareWASMFixture(t, "neural-reed.cicada", 2)
}
