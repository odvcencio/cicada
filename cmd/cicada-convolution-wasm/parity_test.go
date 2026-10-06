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
	"m31labs.dev/cicada/kernel/fx/convolution"
)

func convolutionModulePath() string {
	if path := os.Getenv("CICADA_CONVOLUTION_WASM_PATH"); path != "" {
		return path
	}
	return filepath.Join("..", "..", "build", "cicada-convolution.wasm")
}

// TestConvolutionWASMParity exercises both nonuniform and uniform convolution
// with changing host block boundaries. PCM transfer and initialization finish
// before the memory-growth check begins.
func TestConvolutionWASMParity(t *testing.T) {
	wasm, err := os.ReadFile(convolutionModulePath())
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
	profiles := []struct {
		name              string
		frames, partition int
	}{
		{"nonuniform", 13000, 128},
		{"short-uniform", 257, 256},
		{"long-uniform", 13000, 2048},
	}
	for _, rate := range []int{44100, 48000, 96000} {
		for _, profile := range profiles {
			t.Run(fmt.Sprintf("%d/%s", rate, profile.name), func(t *testing.T) {
				module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
				if err != nil {
					t.Fatal(err)
				}
				defer module.Close(ctx)
				call := func(name string, args ...uint64) uint64 {
					t.Helper()
					fn := module.ExportedFunction(name)
					if fn == nil {
						t.Fatalf("missing convolution export %s", name)
					}
					result, err := fn.Call(ctx, args...)
					if err != nil {
						t.Fatalf("convolution export %s: %v", name, err)
					}
					if len(result) == 0 {
						return 0
					}
					return result[0]
				}
				call("_initialize")
				left, right := make([]float32, profile.frames), make([]float32, profile.frames)
				for i := range left {
					left[i] = float32(.02 * math.Cos(float64(i)*.113) * math.Exp(-float64(i)/4000))
					right[i] = float32(.015 * math.Sin(float64(i)*.173) * math.Exp(-float64(i)/3500))
				}
				native, err := convolution.New(rate, left, right, profile.partition)
				if err != nil {
					t.Fatal(err)
				}
				leftPtr := uint32(call("cicada_ir_alloc", uint64(profile.frames)))
				rightPtr := uint32(call("cicada_ir_right_ptr"))
				memory := module.Memory()
				if leftPtr == 0 || rightPtr == 0 || memory == nil {
					t.Fatal("convolution impulse memory unavailable")
				}
				transfer := make([]byte, profile.frames*4)
				for channel, samples := range [][]float32{left, right} {
					for i, x := range samples {
						binary.LittleEndian.PutUint32(transfer[i*4:], math.Float32bits(x))
					}
					ptr := leftPtr
					if channel == 1 {
						ptr = rightPtr
					}
					if !memory.Write(ptr, transfer) {
						t.Fatal("convolution impulse transfer failed")
					}
				}
				if got := call("cicada_ir_init", uint64(rate), uint64(profile.partition)); got != 0 {
					t.Fatalf("convolution preparation status %d", got)
				}
				if got := int(call("cicada_ir_latency")); got != native.LatencyFrames() {
					t.Fatalf("WASM latency %d, native %d", got, native.LatencyFrames())
				}
				pcmPtr := uint32(call("cicada_ir_pcm_ptr"))
				preparedBytes := memory.Size()
				const inputFrames = 5237
				frames := inputFrames + profile.frames - 1 + profile.partition + 4096
				blocks := [...]int{1, 127, 17, 64, 128, 3, 97, 31, 128, 11}
				var expected [2][128]float32
				var pcm [256 * 4]byte
				var maximum float64
				for base, block := 0, 0; base < frames; block++ {
					count := blocks[block%len(blocks)]
					if count > frames-base {
						count = frames - base
					}
					clear(pcm[:])
					for i := 0; i < count; i++ {
						var l, rr float32
						frame := base + i
						if frame < inputFrames {
							l = float32(.17 * math.Sin(2*math.Pi*273*float64(frame)/float64(rate)))
							rr = float32(.13 * math.Cos(2*math.Pi*411*float64(frame)/float64(rate)))
							if frame%997 == 0 {
								l += .1
							}
						}
						binary.LittleEndian.PutUint32(pcm[i*4:], math.Float32bits(l))
						binary.LittleEndian.PutUint32(pcm[(128+i)*4:], math.Float32bits(rr))
						expected[0][i], expected[1][i] = native.Process(l, rr)
					}
					if !memory.Write(pcmPtr, pcm[:]) || call("cicada_ir_process", uint64(count)) != 0 {
						t.Fatalf("convolution render rejected at frame %d", base)
					}
					for channel := range expected {
						for i := 0; i < count; i++ {
							actual, ok := memory.ReadFloat32Le(pcmPtr + uint32((channel*128+i)*4))
							if !ok || math.IsNaN(float64(actual)) || math.IsInf(float64(actual), 0) {
								t.Fatal("invalid convolution output")
							}
							maximum = math.Max(maximum, math.Abs(float64(actual)-float64(expected[channel][i])))
							if maximum > 1e-6 {
								t.Fatalf("frame %d channel %d parity error %g", base+i, channel, maximum)
							}
						}
					}
					if native.Fault() || memory.Size() != preparedBytes {
						t.Fatalf("render fault or memory growth at frame %d: %d -> %d", base, preparedBytes, memory.Size())
					}
					base += count
				}
				// Leave input and an in-progress tail job, then verify reset
				// removes every queue, history entry and overlap exactly.
				for i := range pcm {
					pcm[i] = 0
				}
				for i := 0; i < 256; i++ {
					binary.LittleEndian.PutUint32(pcm[i*4:], math.Float32bits(.01))
				}
				for block := 0; block < 19; block++ {
					if !memory.Write(pcmPtr, pcm[:]) || call("cicada_ir_process", 128) != 0 {
						t.Fatal("convolution reset excitation failed")
					}
					for i := 0; i < 128; i++ {
						native.Process(.01, .01)
					}
				}
				call("cicada_ir_reset")
				native.Reset()
				clear(pcm[:])
				for block := 0; block < 64; block++ {
					if !memory.Write(pcmPtr, pcm[:]) || call("cicada_ir_process", 128) != 0 {
						t.Fatal("convolution reset render failed")
					}
					for i := 0; i < 128; i++ {
						l, rr := native.Process(0, 0)
						for channel := 0; channel < 2; channel++ {
							actual, ok := memory.ReadFloat32Le(pcmPtr + uint32((channel*128+i)*4))
							if !ok || actual != 0 || l != 0 || rr != 0 {
								t.Fatal("reset did not produce exact silence")
							}
						}
					}
					if memory.Size() != preparedBytes {
						t.Fatal("WASM memory grew during reset rendering")
					}
				}
				t.Logf("maximum parity error %g; latency %d; memory %d bytes, no growth", maximum, native.LatencyFrames(), preparedBytes)
			})
		}
	}
}
