//go:build wasm_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// TestAudioWASMExamplesPCM24Parity gates complete arrangements and their tails.
// CI saves amd64 PCM and supplies it to the same test running natively on arm64.
func TestAudioWASMExamplesPCM24Parity(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.cicada"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("discover examples: %v (%d scores)", err, len(paths))
	}
	output := os.Getenv("CICADA_PARITY_DIR")
	if output == "" {
		output = t.TempDir()
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	reference := os.Getenv("CICADA_PARITY_REFERENCE")
	if reference != "" {
		files, err := filepath.Glob(filepath.Join(reference, "*.pcm24"))
		if err != nil || len(files) != len(paths)*2 {
			t.Fatalf("reference corpus: %v (%d files, want %d)", err, len(files), len(paths)*2)
		}
	}
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	compiled, err := r.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			score, diagnostics, err := project.LoadScore(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			p, diagnostics := project.FromScore(score)
			if p == nil {
				t.Fatalf("project: %v", diagnostics)
			}
			for _, rate := range []int{44_100, 48_000} {
				t.Run(sampleRateName(rate), func(t *testing.T) {
					const blockSize = 128
					cfg, err := project.CompileEngine(p, rate, blockSize)
					if err != nil {
						t.Fatal(err)
					}
					native, err := engine.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					image, err := kernelimage.Encode(cfg)
					if err != nil {
						t.Fatal(err)
					}
					module, err := r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
					if err != nil {
						t.Fatal(err)
					}
					defer module.Close(ctx)
					call := func(name string, args ...uint64) uint64 {
						t.Helper()
						fn := module.ExportedFunction(name)
						if fn == nil {
							t.Fatalf("missing WASM export %s", name)
						}
						result, err := fn.Call(ctx, args...)
						if err != nil {
							t.Fatalf("%s: %v", name, err)
						}
						if len(result) == 0 {
							return 0
						}
						return result[0]
					}
					call("_initialize")
					ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
					if ptr == 0 || !module.Memory().Write(ptr, image) {
						t.Fatal("project image buffer unavailable")
					}
					if call("gosx_audio_init", uint64(rate), blockSize, 2) != 0 {
						t.Fatal("WASM init failed")
					}
					play := cmd.Command{Op: cmd.OpPlay, Track: 0xff}
					record, err := cmd.EncodeCommand(play, uint8(cfg.Tracks))
					if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) || !native.Push(play) {
						t.Fatalf("play command: %v", err)
					}
					call("gosx_audio_cmd_commit", 1)
					outPtr := uint32(call("gosx_audio_out_ptr"))
					msgPtr := uint32(call("gosx_audio_msg_ptr"))
					memorySize := module.Memory().Size()
					allocBytes := call("gosx_audio_alloc_bytes")
					bars := 0
					for _, entry := range cfg.Song {
						bars += int(entry.Bars)
					}
					clock, err := seq.NewClock(rate, cfg.BPMMilli)
					if err != nil || bars == 0 {
						t.Fatalf("song clock: %v, bars=%d", err, bars)
					}
					frames := int(clock.SampleAtTick(int64(bars)*seq.TicksPerBar)) + 3*rate
					name := fmt.Sprintf("%s-%d.pcm24", filepath.Base(path), rate)
					file, err := os.Create(filepath.Join(output, name))
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					var expected *os.File
					if reference != "" {
						expected, err = os.Open(filepath.Join(reference, name))
						if err != nil {
							t.Fatal(err)
						}
						defer expected.Close()
					}
					hash := sha256.New()
					var left, right [blockSize]float32
					var nativePCM, wasmPCM, referencePCM [blockSize * 6]byte
					var sounded bool
					for at := 0; at < frames; at += blockSize {
						n := min(blockSize, frames-at)
						native.Render(left[:n], right[:n])
						call("gosx_audio_render", uint64(n))
						for i := 0; i < n; i++ {
							for channel, value := range [...]float32{left[i], right[i]} {
								wasmValue, ok := module.Memory().ReadFloat32Le(outPtr + uint32((channel*blockSize+i)*4))
								if !ok {
									t.Fatal("WASM output out of bounds")
								}
								if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || math.IsNaN(float64(wasmValue)) || math.IsInf(float64(wasmValue), 0) {
									t.Fatalf("nonfinite sample at frame %d channel %d", at+i, channel)
								}
								index := i*6 + channel*3
								encodeParityPCM24(nativePCM[index:index+3], value)
								encodeParityPCM24(wasmPCM[index:index+3], wasmValue)
								if !bytes.Equal(nativePCM[index:index+3], wasmPCM[index:index+3]) {
									t.Fatalf("native/WASM PCM24 differs at frame %d channel %d: %x / %x (float32 %.9g / %.9g)", at+i, channel, nativePCM[index:index+3], wasmPCM[index:index+3], value, wasmValue)
								}
								sounded = sounded || value != 0
							}
						}
						pcm := nativePCM[:n*6]
						if expected != nil {
							if _, err := io.ReadFull(expected, referencePCM[:len(pcm)]); err != nil {
								t.Fatalf("amd64 reference at frame %d: %v", at, err)
							}
							if !bytes.Equal(pcm, referencePCM[:len(pcm)]) {
								t.Fatalf("amd64/%s PCM24 differs in block at frame %d", runtime.GOARCH, at)
							}
						}
						if _, err := file.Write(pcm); err != nil {
							t.Fatal(err)
						}
						_, _ = hash.Write(pcm)
						for _, message := range drainNativeMessages(native) {
							if message.Kind == cmd.Fault {
								t.Fatalf("native fault: %+v", message)
							}
						}
						count := int(call("gosx_audio_msg_drain"))
						for i := 0; i < count; i++ {
							data, ok := module.Memory().Read(msgPtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
							if !ok {
								t.Fatal("WASM message out of bounds")
							}
							message, err := cmd.DecodeMessage(data)
							if err != nil || message.Kind == cmd.Fault {
								t.Fatalf("WASM fault: %+v, %v", message, err)
							}
						}
					}
					if expected != nil {
						var extra [1]byte
						if n, err := expected.Read(extra[:]); n != 0 || err != io.EOF {
							t.Fatal("amd64 reference has trailing PCM")
						}
					}
					if !sounded || module.Memory().Size() != memorySize || call("gosx_audio_alloc_bytes") != allocBytes {
						t.Fatal("silent render, WASM memory growth, or render allocation")
					}
					t.Logf("PCM24 %s native/WASM byte-identical: bars=%d tail=3s rate=%d frames=%d sha256=%x", runtime.GOARCH, bars, rate, frames, hash.Sum(nil))
				})
			}
		})
	}
}

// Match the undithered integer WAV encoder: scale, ties-to-even, saturate.
func encodeParityPCM24(dst []byte, value float32) {
	pcm := int32(math.RoundToEven(float64(value) * 8388607))
	pcm = max(-8388608, min(8388607, pcm))
	dst[0], dst[1], dst[2] = byte(pcm), byte(pcm>>8), byte(pcm>>16)
}
