//go:build wasm_integration

package main

import (
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestInstrumentVibratoNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
instrument lead { vibrato = 30ct vibrato_rate = 6Hz vibrato_delay = 100ms voice mono { out = sine(pitch) * env(gate, 300ms) } }
track voice lead { vibrato = 40ct }
track bass acid { vibrato = 15ct vibrato_rate = 4Hz vibrato_delay = 50ms }
pattern p { 1~ 3 . 5 vibrato: 5ct . 0ct . }
scene main { voice = p bass = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "vibrato-instrument", p, 1, true, wasm, []int{44100, 48000})
}
