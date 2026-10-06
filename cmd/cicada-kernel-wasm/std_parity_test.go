//go:build wasm_integration

package main

import (
	"os"
	"testing"

	"m31labs.dev/cicada/internal/stdtest"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMStdSampleParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range stdtest.Libraries {
		t.Run(name, func(t *testing.T) {
			_, inline, p := stdtest.Load(t, "../..", name)
			q, ds := project.FromScore(inline)
			if q == nil || len(ds) != 0 {
				t.Fatalf("inline compile: %+v", ds)
			}
			importedPCM := compareWASMProject(t, "std/"+name, p, 2, true, wasm)
			inlinePCM := compareWASMProject(t, "std/"+name+"-inline", q, 2, true, wasm)
			for _, rate := range []int{44100, 48000} {
				if importedPCM[rate] != inlinePCM[rate] {
					t.Fatalf("std/%s and inline WASM PCM bytes differ at %d Hz", name, rate)
				}
				t.Logf("METRIC std/%s rate=%d wasm_inline_pcm=byte-identical", name, rate)
			}
		})
	}
}
