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

func TestChorusNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
fx wide chorus { voices = 3 rate = 0.6Hz depth = 3ms spread = 1 mix = 0.5 }
track lead acid { insert = wide }
pattern p { 1 . 3 . }
lfo motion { rate = 1/4 }
mod motion -> lead.chorus_mix depth = 0.4
scene main { lead = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "chorus-modulated", p, 1, true, wasm, []int{44100, 48000, 96000})
}

func TestChorusInstrumentModulatorNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
lfo motion { wave = triangle rate = 1/4 }
instrument lead { voice mono { out = sine(pitch) * (0.5 + 0.4 * mod(motion)) } }
fx wide chorus {}
track voice lead { insert = wide }
pattern p { 1 . 3 . }
mod motion -> voice.chorus_mix depth = 0.2
scene main { voice = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "chorus-instrument-modulation", p, 1, true, wasm, []int{44100, 48000, 96000})
}

func TestKeysChorusNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_KEYS_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_KEYS_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
fx wide chorus {}
track keys tine_ep { insert = wide }
pattern chord notes { c4 . e4 . }
lfo motion { rate = 1/4 }
mod motion -> keys.chorus_mix depth = 0.2
scene main { keys = chord }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	compareWASMProjectRates(t, "chorus-keys", p, 1, true, wasm, []int{44100, 48000, 96000})
}

func TestChorusRejectedByCoreKernel(t *testing.T) {
	path := os.Getenv("CICADA_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
fx wide chorus {}
track lead acid { insert = wide }
pattern p { 1 . }
scene main { lead = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompilePortableEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		result, err := module.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 0 {
			return result[0]
		}
		return 0
	}
	call("_initialize")
	ptr := uint32(call("gosx_audio_project_alloc", uint64(len(data))))
	if ptr == 0 || !module.Memory().Write(ptr, data) {
		t.Fatal("image upload failed")
	}
	if call("gosx_audio_init", 48000, 128, 2) == 0 {
		t.Fatal("core accepted chorus image")
	}
}
