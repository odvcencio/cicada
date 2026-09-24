//go:build wasm_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/phrase"
)

func TestPhraseWASMParity(t *testing.T) {
	path := filepath.Join("..", "..", "build", "cicada-phrase.wasm")
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		fn := module.ExportedFunction(name)
		if fn == nil {
			t.Fatalf("missing WASM export %s", name)
		}
		values, err := fn.Call(ctx, args...)
		if err != nil {
			t.Fatalf("WASM %s: %v", name, err)
		}
		if len(values) == 0 {
			return 0
		}
		return values[0]
	}
	call("_initialize")
	check := func(t *testing.T, params phrase.Params) {
		t.Helper()
		native, err := phrase.Generate(params)
		if err != nil {
			t.Fatalf("native seed %d: %v", params.Seed, err)
		}
		wantTrace, err := json.Marshal(native.Trace)
		if err != nil {
			t.Fatal(err)
		}
		input, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		ptr := uint32(call("cicada_phrase_params_alloc", uint64(len(input))))
		if ptr == 0 || !module.Memory().Write(ptr, input) {
			t.Fatalf("WASM params buffer unavailable for seed %d", params.Seed)
		}
		status := call("cicada_phrase_generate")
		resultPtr := uint32(call("cicada_phrase_result_ptr"))
		resultLen := uint32(call("cicada_phrase_result_len"))
		result, ok := module.Memory().Read(resultPtr, resultLen)
		if !ok || status != 0 {
			t.Fatalf("WASM seed %d status %d: %s", params.Seed, status, result)
		}
		separator := bytes.IndexByte(result, 0)
		if separator < 0 {
			t.Fatalf("WASM seed %d omitted trace separator", params.Seed)
		}
		if !bytes.Equal(result[:separator], []byte(native.Notation)) {
			t.Fatalf("WASM seed %d source differs from native", params.Seed)
		}
		if !bytes.Equal(result[separator+1:], wantTrace) {
			t.Fatalf("WASM seed %d draw trace differs from native", params.Seed)
		}
	}
	for seed := uint64(0); seed < 24; seed++ {
		params := phrase.DefaultParams()
		params.Seed, params.Key, params.Scale = seed, 9, phrase.Minor
		t.Run(fmt.Sprintf("fixture-%02d", seed), func(t *testing.T) { check(t, params) })
	}
	for scale := phrase.Minor; scale <= phrase.Blues; scale++ {
		params := phrase.DefaultParams()
		params.Seed, params.Scale = 4242, scale
		t.Run(fmt.Sprintf("scale-%d", scale), func(t *testing.T) { check(t, params) })
	}
	for _, structure := range []phrase.Structure{phrase.A, phrase.AABA, phrase.ABAB, phrase.ABAC, phrase.AAAB} {
		params := phrase.DefaultParams()
		params.Seed, params.Structure = 8675309, structure
		t.Run(fmt.Sprintf("structure-%d", structure), func(t *testing.T) { check(t, params) })
	}
	for _, steps := range []uint8{8, 16, 32, 64} {
		params := phrase.DefaultParams()
		params.Seed, params.Steps = 1234, steps
		params.Density, params.AccentDensity, params.SlideDensity, params.OctaveJump = .75, .25, .6, .5
		t.Run(fmt.Sprintf("steps-%d", steps), func(t *testing.T) { check(t, params) })
	}
}
