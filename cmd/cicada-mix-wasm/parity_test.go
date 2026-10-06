//go:build wasm_integration

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"m31labs.dev/cicada/kernel/fx/pro"
)

func mixWASMPath() string {
	if path := os.Getenv("CICADA_MIX_WASM_PATH"); path != "" {
		return path
	}
	return filepath.Join("..", "..", "build", "cicada-mix.wasm")
}

func mixCall(t *testing.T, ctx context.Context, module api.Module, name string, args ...uint64) uint32 {
	t.Helper()
	function := module.ExportedFunction(name)
	if function == nil {
		t.Fatalf("missing mix export %s", name)
	}
	values, err := function.Call(ctx, args...)
	if err != nil {
		t.Fatalf("mix export %s: %v", name, err)
	}
	if len(values) == 0 {
		return 0
	}
	return uint32(values[0])
}

func TestMixWASMNativeParity(t *testing.T) {
	wasm, err := os.ReadFile(mixWASMPath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		for preset, name := range pro.PresetNames {
			t.Run(fmt.Sprintf("%s/%d", name, rate), func(t *testing.T) {
				module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = module.Close(ctx) })
				mixCall(t, ctx, module, "_initialize")
				if status := mixCall(t, ctx, module, "cicada_mix_init", uint64(rate), uint64(preset)); status != 0 {
					t.Fatalf("mix init status %d", status)
				}
				params, _ := pro.Preset(name)
				native, err := pro.New(rate, params)
				if err != nil {
					t.Fatal(err)
				}
				if got := mixCall(t, ctx, module, "cicada_mix_latency"); got != uint32(native.LatencyFrames()) {
					t.Fatalf("latency differs: native=%d WASM=%d", native.LatencyFrames(), got)
				}
				pointer := mixCall(t, ctx, module, "cicada_mix_pcm_ptr")
				initialMemory := module.Memory().Size()
				var pcm [1024]byte
				var expected [2][128]float32
				frame, maxError := 0, 0.0
				// Vary host block boundaries while preserving signal/sample time.
				for block := 0; block < 160; block++ {
					count := []int{1, 17, 64, 128}[block%4]
					clear(pcm[:])
					for i := 0; i < count; i++ {
						x := .2*math.Sin(2*math.Pi*997*float64(frame)/float64(rate)) + .08*math.Sin(2*math.Pi*6301*float64(frame)/float64(rate))
						if frame%641 > 210 && frame%641 < 350 {
							x *= 8
						}
						left, right := float32(x), float32(-x*.47)
						expected[0][i], expected[1][i] = native.Process(left, right)
						binary.LittleEndian.PutUint32(pcm[i*4:], math.Float32bits(left))
						binary.LittleEndian.PutUint32(pcm[(128+i)*4:], math.Float32bits(right))
						frame++
					}
					if !module.Memory().Write(pointer, pcm[:]) {
						t.Fatal("mix PCM write out of bounds")
					}
					if status := mixCall(t, ctx, module, "cicada_mix_process", uint64(count)); status != 0 {
						t.Fatalf("mix process status %d", status)
					}
					for channel := 0; channel < 2; channel++ {
						for i := 0; i < count; i++ {
							got, ok := module.Memory().ReadFloat32Le(pointer + uint32((128*channel+i)*4))
							if !ok || math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) {
								t.Fatal("mix PCM read is invalid")
							}
							difference := math.Abs(float64(got - expected[channel][i]))
							maxError = math.Max(maxError, difference)
							if difference > 1e-5 {
								t.Fatalf("channel=%d frame=%d native=%g WASM=%g error=%g", channel, frame-count+i, expected[channel][i], got, difference)
							}
						}
					}
				}
				if got := module.Memory().Size(); got != initialMemory {
					t.Fatalf("render memory grew: %d -> %d", initialMemory, got)
				}
				t.Logf("%d frames; max absolute error %.3g; linear memory %d bytes", frame, maxError, initialMemory)
			})
		}
	}
}

func TestMixWASMValidationAndFaultReset(t *testing.T) {
	wasm, err := os.ReadFile(mixWASMPath())
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
	mixCall(t, ctx, module, "_initialize")
	for _, call := range []struct {
		name string
		args []uint64
	}{
		{"cicada_mix_process", []uint64{128}},
		{"cicada_mix_init", []uint64{32000, 0}},
		{"cicada_mix_init", []uint64{48000, math.MaxUint32}},
		{"cicada_mix_init", []uint64{48000, uint64(len(pro.PresetNames))}},
	} {
		if got := mixCall(t, ctx, module, call.name, call.args...); got != math.MaxUint32 {
			t.Fatalf("invalid %s accepted: %d", call.name, got)
		}
	}
	if got := mixCall(t, ctx, module, "cicada_mix_init", 48000, 6); got != 0 {
		t.Fatalf("valid init failed: %d", got)
	}
	for _, frames := range []uint64{0, 129, math.MaxUint32} {
		if got := mixCall(t, ctx, module, "cicada_mix_process", frames); got != math.MaxUint32 {
			t.Fatalf("invalid frame count %d accepted", frames)
		}
	}
	if got := mixCall(t, ctx, module, "cicada_mix_init", 48000, 0); got != math.MaxUint32 {
		t.Fatal("second initialization accepted")
	}
	pointer := mixCall(t, ctx, module, "cicada_mix_pcm_ptr")
	if !module.Memory().WriteUint32Le(pointer, math.Float32bits(float32(math.NaN()))) {
		t.Fatal("could not write fault input")
	}
	if got := mixCall(t, ctx, module, "cicada_mix_process", 128); got != math.MaxUint32 {
		t.Fatal("nonfinite input was accepted")
	}
	mixCall(t, ctx, module, "cicada_mix_reset")
	var zeros [1024]byte
	if !module.Memory().Write(pointer, zeros[:]) || mixCall(t, ctx, module, "cicada_mix_process", 128) != 0 {
		t.Fatal("fault reset failed")
	}
	for i := 0; i < 256; i++ {
		value, ok := module.Memory().ReadFloat32Le(pointer + uint32(i*4))
		if !ok || value != 0 {
			t.Fatal("reset-silence is nonzero")
		}
	}
}
