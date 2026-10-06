//go:build wasm_integration

package main

import "testing"

func TestAudioWASMModeledPercussionParity(t *testing.T) {
	for _, name := range []string{"steelpan", "marimba", "vibraphone", "conga", "shaker", "wood", "zinc", "felt", "marble"} {
		t.Run(name, func(t *testing.T) { compareWASMFixture(t, "modeled/"+name+".cicada", 1) })
	}
}
