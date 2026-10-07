//go:build wasm_integration

package main

import "testing"

func TestAudioWASMSourceChainSampleParity(t *testing.T) {
	compareWASMFixture(t, "section-chain.cicada", 2)
}
