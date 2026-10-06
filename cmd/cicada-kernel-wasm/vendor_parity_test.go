//go:build wasm_integration

package main

import (
	"m31labs.dev/cicada/internal/vendortest"
	"m31labs.dev/cicada/project"
	"os"
	"testing"
)

func TestAudioWASMVendoredSampleParity(t *testing.T) {
	_, after, p := vendortest.BeforeAndAfter(t)
	q, ds := project.FromScore(after)
	if q == nil || len(ds) != 0 {
		t.Fatalf("vendored compile: %+v", ds)
	}
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	beforePCM := compareWASMProject(t, "libraries-user", p, 2, true, wasm)
	afterPCM := compareWASMProject(t, "libraries-vendored", q, 2, true, wasm)
	for _, rate := range []int{44100, 48000} {
		if beforePCM[rate] != afterPCM[rate] {
			t.Fatalf("vendoring changed WASM PCM at %d Hz", rate)
		}
		t.Logf("METRIC libraries-vendored rate=%d wasm_pcm=byte-identical user_library=deleted", rate)
	}
}
