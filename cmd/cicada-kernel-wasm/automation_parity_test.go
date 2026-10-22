//go:build wasm_integration

package main

import "testing"

func TestAudioWASMContinuousAutomationParity(t *testing.T) {
	compareWASMFixture(t, "continuous-automation.cicada", 4)
}

func TestAudioWASMArrangementTempoParity(t *testing.T) {
	compareWASMFixture(t, "arrangement-tempo.cicada", 6)
}

func TestAudioWASMAutomationRecallParity(t *testing.T) {
	const source = `cicada 2
tempo 120
seed 42
track bass acid { level = -18dB }
pattern pulse { c2 . g2 . }
arrange { place bass bass pulse { at = @1.1.1 length = 4bars } }
automate bass.level { @1.1.1 -18dB step recall @2.1.1 recall @3.1.1 }
automate bass.cutoff { @1.1.1 400Hz @5.1.1 3200Hz smooth recall @2.1.1 recall @4.1.1 }
`
	compareWASMSource(t, "automation-recalls", []byte(source), 4, true)
}
