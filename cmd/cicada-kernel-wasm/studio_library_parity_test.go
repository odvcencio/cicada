//go:build wasm_integration

package main

import (
	"m31labs.dev/cicada/internal/studiolibtest"
	"m31labs.dev/cicada/project"
	"os"
	"testing"
)

func TestAudioWASMStudioLibraryParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	_, inline, p := studiolibtest.Load(t, "../..")
	q, ds := project.FromScore(inline)
	if q == nil || len(ds) != 0 {
		t.Fatalf("inline: %+v", ds)
	}
	a := compareWASMProject(t, "libraries-studio", p, 2, true, wasm)
	b := compareWASMProject(t, "libraries-studio-inline", q, 2, true, wasm)
	for _, rate := range []int{44100, 48000} {
		if a[rate] != b[rate] {
			t.Fatalf("WASM bytes differ at %d Hz", rate)
		}
		t.Logf("METRIC studio-library rate=%d wasm_inline_pcm=byte-identical", rate)
	}
}
