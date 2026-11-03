//go:build wasm_integration

package main

import (
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestKeysModularWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_KEYS_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_KEYS_MODULAR_WASM_PATH to the combined optional module")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
tempo 240
track bass acid {}
track keys tine_ep {}
pattern pulse acid { 1 . 5 . }
pattern chord notes { c4 . e4 . }
scene main { bass = pulse keys = chord }
song { main }
lfo drift { rate = 1/4 wave = triangle }
mod drift -> bass.pan depth = 0.2
`))
	if score == nil {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "keys-modular", p, 1, true, wasm, []int{44_100, 48_000, 96_000})
}
