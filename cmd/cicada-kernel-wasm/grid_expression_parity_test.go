//go:build wasm_integration

package main

import (
	"os"
	"testing"
)

func TestAudioWASMGridChainVelocityExpressionAutomationParity(t *testing.T) {
	source, err := os.ReadFile("../../examples/grid-expression-chain.cicada")
	if err != nil {
		t.Fatal(err)
	}
	compareWASMSource(t, "grid-expression-chain.cicada", source, 2, true)
}
