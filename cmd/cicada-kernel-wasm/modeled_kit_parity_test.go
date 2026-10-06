//go:build wasm_integration

package main

import (
	"fmt"
	"testing"

	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

func TestAudioWASMModeledKitParity(t *testing.T) {
	for _, name := range modeledkit.Names {
		t.Run(name, func(t *testing.T) {
			source := fmt.Sprintf("cicada 2\nseed 73\ntempo 120\nkit acoustic { bd = model.%s }\ntrack drums acoustic { bd_tune = 1.15 bd_decay = 0.7 bd_position = 0.62 bd_humanize = 0.03 }\npattern beat drums { bd: x2...x4...x7...X... }\nscene main { drums = beat }\nsong { main }\n", name)
			compareWASMSource(t, "modeled-kit/"+name, []byte(source), 1, false)
		})
	}
}

func TestAudioWASMModeledKitGrooveParity(t *testing.T) {
	for _, name := range []string{"reggae-one-drop", "soca", "rock", "jazz-ride"} {
		t.Run(name, func(t *testing.T) { compareWASMFixture(t, "modeled-kit/"+name+"-after.cicada", 1) })
	}
}
