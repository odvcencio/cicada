//go:build wasm_integration

package main

import (
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGraphLFONativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
seed 4242
instrument lead { voice mono { out = sine(pitch) * (0.5 + 0.4 * lfo(2Hz, triangle)) } }
instrument pad { voice poly { out = sine(pitch) * (0.5 + 0.4 * lfo(3Hz, sample-hold, note)) } }
track voice lead {}
track chords pad {}
pattern p { c4 . e4 . }
scene main { voice = p chords = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "graph-lfo", p, 1, true, wasm, []int{44100, 48000, 96000})
	corePath := os.Getenv("CICADA_WASM_PATH")
	if corePath == "" {
		t.Fatal("set CICADA_WASM_PATH to verify core rejection")
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.Instantiate(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		results, err := module.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 0 {
			return results[0]
		}
		return 0
	}
	call("_initialize")
	ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if ptr == 0 || !module.Memory().Write(ptr, image) {
		t.Fatal("image upload failed")
	}
	if call("gosx_audio_init", 48000, 128, 2) == 0 {
		t.Fatal("core accepted a graph LFO image")
	}
}

func TestGraphLFOKeysNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_KEYS_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_KEYS_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
instrument lead { voice mono { out = sine(pitch) * (0.5 + 0.4 * lfo(2Hz, sine, note)) } }
track voice lead {}
track keys tine_ep {}
pattern p { c4 . e4 . }
scene main { voice = p keys = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "graph-lfo", p, 1, true, wasm, []int{44100, 48000, 96000})
}
