//go:build wasm_integration

package main

import "testing"

func TestAudioWASMContinuousAutomationParity(t *testing.T) {
	compareWASMFixture(t, "continuous-automation.cicada", 4)
}
