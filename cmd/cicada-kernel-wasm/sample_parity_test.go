//go:build wasm_integration

package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMFirstAcidSampleParity(t *testing.T) {
	compareWASMFixture(t, "first-acid.cicada", 8)
}

func TestAudioWASMAuthoredKitSampleParity(t *testing.T) {
	compareWASMFixture(t, "authored-kit.cicada", 1)
}

func compareWASMFixture(t *testing.T, fixture string, bars int) {
	t.Helper()
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", fixture))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	for _, rate := range []int{44_100, 48_000} {
		t.Run(sampleRateName(rate), func(t *testing.T) {
			const blockSize = 128
			cfg, err := project.CompileEngine(p, rate, blockSize)
			if err != nil {
				t.Fatal(err)
			}
			image, err := kernelimage.Encode(cfg)
			if err != nil {
				t.Fatal(err)
			}
			native, err := engine.New(cfg)
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
				result, err := fn.Call(ctx, args...)
				if err != nil {
					t.Fatalf("WASM %s: %v", name, err)
				}
				if len(result) == 0 {
					return 0
				}
				return result[0]
			}
			call("_initialize")
			imagePtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if imagePtr == 0 || !module.Memory().Write(imagePtr, image) {
				t.Fatal("project image buffer unavailable")
			}
			if call("gosx_audio_init", uint64(rate), blockSize, 2) != 0 {
				t.Fatal("WASM project init failed")
			}
			play := cmd.Command{Op: cmd.OpPlay, Track: 0xff}
			record, err := cmd.EncodeCommand(play, uint8(cfg.Tracks))
			if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) {
				t.Fatalf("write play command: %v", err)
			}
			call("gosx_audio_cmd_commit", 1)
			if !native.Push(play) {
				t.Fatal("native play command rejected")
			}
			outputPtr := uint32(call("gosx_audio_out_ptr"))
			messagePtr := uint32(call("gosx_audio_msg_ptr"))
			frames := int(math.Round(float64(bars*4*60*rate*1000) / float64(cfg.BPMMilli)))
			var nativeL, nativeR [blockSize]float32
			var wasmEvents, nativeEvents []cmd.Message
			var peakDifference float64
			var peakSample int
			var peakChannel int
			var nonzero bool
			var stableMemory uint32
			for block := 0; block*blockSize < frames; block++ {
				call("gosx_audio_render", blockSize)
				native.Render(nativeL[:], nativeR[:])
				for channel := 0; channel < 2; channel++ {
					for frame := 0; frame < blockSize && block*blockSize+frame < frames; frame++ {
						wasmSample, ok := module.Memory().ReadFloat32Le(outputPtr + uint32((channel*blockSize+frame)*4))
						if !ok {
							t.Fatal("WASM output pointer out of bounds")
						}
						nativeSample := nativeL[frame]
						if channel == 1 {
							nativeSample = nativeR[frame]
						}
						if math.IsNaN(float64(wasmSample)) || math.IsInf(float64(wasmSample), 0) {
							t.Fatalf("nonfinite WASM sample at %d channel %d", block*blockSize+frame, channel)
						}
						nonzero = nonzero || wasmSample != 0
						difference := math.Abs(float64(wasmSample) - float64(nativeSample))
						if difference > peakDifference {
							peakDifference, peakSample, peakChannel = difference, block*blockSize+frame, channel
						}
					}
				}
				if block%64 == 63 || (block+1)*blockSize >= frames {
					count := int(call("gosx_audio_msg_drain"))
					for i := 0; i < count; i++ {
						data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
						if !ok {
							t.Fatal("WASM message pointer out of bounds")
						}
						message, err := cmd.DecodeMessage(data)
						if err != nil || message.Kind == cmd.Fault {
							t.Fatalf("WASM message: %+v %v", message, err)
						}
						wasmEvents = append(wasmEvents, message)
					}
					nativeEvents = append(nativeEvents, drainNativeMessages(native)...)
				}
				if block == 10 {
					stableMemory = module.Memory().Size()
				}
			}
			if !nonzero {
				t.Fatalf("%s rendered silence", fixture)
			}
			if stableMemory == 0 || module.Memory().Size() != stableMemory {
				t.Fatalf("WASM memory grew after warm-up: %d -> %d", stableMemory, module.Memory().Size())
			}
			compareMusicalMessages(t, wasmEvents, nativeEvents)
			if peakDifference > 1e-6 {
				t.Fatalf("native/WASM peak sample difference %.9g at frame %d channel %d exceeds 1e-6", peakDifference, peakSample, peakChannel)
			}
			t.Logf("%s: %d bars at %d Hz, peak native/WASM sample difference %.9g", fixture, bars, rate, peakDifference)
		})
	}
}

func sampleRateName(rate int) string {
	if rate == 44_100 {
		return "44.1kHz"
	}
	return "48kHz"
}
