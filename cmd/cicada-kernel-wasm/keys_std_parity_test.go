//go:build wasm_integration && keys

package main

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMStdKeysImportedSampleParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, patch := range keyboard.Names {
		t.Run(patch, func(t *testing.T) {
			path := filepath.Join("..", "..", "examples", "keys", "std", patch+".cicada")
			score, ds, err := project.LoadScore(path, nil)
			if err != nil || score == nil {
				t.Fatalf("standard keyboard import: %v %+v", err, ds)
			}
			for _, d := range ds {
				if d.Severity == "error" {
					t.Fatal(ds)
				}
			}
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			// Exercise attacks, the comping chord and its release within one test bar.
			p.TempoMilli = 240000
			compareWASMProject(t, "keys-std-"+patch, p, 1, true, wasm)
		})
	}
}
