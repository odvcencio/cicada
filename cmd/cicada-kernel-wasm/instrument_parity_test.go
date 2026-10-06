//go:build wasm_integration

package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The new graph operations and their extra input indices must survive image
// serialization and produce the same samples in TinyGo and the native engine.
// Commands land on common frame boundaries for every tested block size.
func TestAudioWASMPolyphonicInstrumentSampleParity(t *testing.T) {
	const source = `cicada 2
tempo 120
instrument precision {
  voice poly {
    let tone = pulse(pitch, 0.37) * 0.7 + saw(pitch * 1.003) * 0.3
    let amp = adsr(gate, 5ms, 35ms, 0.62, 35ms)
    out = svf(tone, 1200Hz, 0.5) * amp * velocity * 0.07
  }
}
track keys precision {}
pattern quiet notes steps=1 { . }
scene main { keys=quiet }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	wasm, err := os.ReadFile(wasmModulePath())
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
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, blockSize := range []int{64, 128, 256} {
			t.Run(fmt.Sprintf("rate=%d/block=%d", rate, blockSize), func(t *testing.T) {
				cfg, err := project.CompileEngine(p, rate, blockSize)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Track[0].Kind != engine.VoiceGraphPoly {
					t.Fatalf("authored polyphony compiled as voice kind %d", cfg.Track[0].Kind)
				}
				image, err := kernelimage.Encode(cfg)
				if err != nil {
					t.Fatal(err)
				}
				native, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = module.Close(ctx) })
				call := func(name string, args ...uint64) uint64 {
					t.Helper()
					function := module.ExportedFunction(name)
					if function == nil {
						t.Fatalf("missing WASM export %s", name)
					}
					result, err := function.Call(ctx, args...)
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
					t.Fatal("WASM image allocation failed")
				}
				if got := call("gosx_audio_init", uint64(rate), uint64(blockSize), 2); got != 0 {
					t.Fatalf("WASM polyphonic image initialization failed: %d", got)
				}
				commandPtr := uint32(call("gosx_audio_cmd_ptr"))
				push := func(commands ...cmd.Command) {
					t.Helper()
					for i, command := range commands {
						record, err := cmd.EncodeCommand(command, uint8(cfg.Tracks))
						if err != nil || !module.Memory().Write(commandPtr+uint32(i*cmd.CommandSize), record[:]) {
							t.Fatalf("write command %d: %v", i, err)
						}
					}
					call("gosx_audio_cmd_commit", uint64(len(commands)))
					if !native.PushBatch(commands) {
						t.Fatal("native live command batch was rejected")
					}
				}
				noteOn := func(note uint8) cmd.Command {
					return cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: uint32(note) | 100<<8}
				}
				noteOff := func(note uint8) cmd.Command {
					return cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: uint16(note)}
				}
				commands := []cmd.Command{{Op: cmd.OpPlay, Track: 0xff}}
				for _, note := range [...]uint8{48, 55, 60, 64, 67, 72, 76, 79} {
					commands = append(commands, noteOn(note))
				}
				push(commands...)
				outputPtr := uint32(call("gosx_audio_out_ptr"))
				messagePtr := uint32(call("gosx_audio_msg_ptr"))
				left, right := make([]float32, blockSize), make([]float32, blockSize)
				var nativeEvents, wasmEvents []cmd.Message
				var peakDifference, peakOutput, lateEnergy float64
				var stableMemory uint32
				const frames = 16_384
				for offset := 0; offset < frames; offset += blockSize {
					switch offset {
					case 1024:
						push(noteOn(84)) // Ninth note steals one still-held voice.
					case 2048:
						push(noteOff(60)) // Independent release while others stay held.
					case 3072:
						push(noteOn(72)) // A second instance of the same pitch.
					case 4096, 5120:
						push(noteOff(72)) // Ordered note-off pairs release each instance.
					case 7168:
						push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 0xffff})
					}
					call("gosx_audio_render", uint64(blockSize))
					native.Render(left, right)
					for channel, nativeSamples := range [][]float32{left, right} {
						for i, nativeSample := range nativeSamples {
							wasmSample, ok := module.Memory().ReadFloat32Le(outputPtr + uint32((channel*blockSize+i)*4))
							if !ok || math.IsNaN(float64(wasmSample)) || math.IsInf(float64(wasmSample), 0) {
								t.Fatalf("invalid WASM sample at %d/channel%d", offset+i, channel)
							}
							peakDifference = math.Max(peakDifference, math.Abs(float64(wasmSample)-float64(nativeSample)))
							peakOutput = math.Max(peakOutput, math.Abs(float64(wasmSample)))
							if offset >= frames-1024 {
								lateEnergy += float64(wasmSample) * float64(wasmSample)
							}
						}
					}
					count := int(call("gosx_audio_msg_drain"))
					for i := 0; i < count; i++ {
						data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
						if !ok {
							t.Fatal("WASM message pointer out of bounds")
						}
						message, err := cmd.DecodeMessage(data)
						if err != nil || message.Kind == cmd.Fault {
							t.Fatalf("WASM polyphonic message: %+v %v", message, err)
						}
						wasmEvents = append(wasmEvents, message)
					}
					for _, message := range drainNativeMessages(native) {
						if message.Kind == cmd.Fault {
							t.Fatalf("native polyphonic fault: %+v", message)
						}
						nativeEvents = append(nativeEvents, message)
					}
					if offset == 1280 {
						stableMemory = module.Memory().Size()
					}
				}
				if peakOutput < .001 || peakDifference > 1e-6 {
					t.Fatalf("polyphonic parity: peak output %g, maximum native/WASM difference %g", peakOutput, peakDifference)
				}
				if lateEnergy > 1e-10 {
					t.Fatalf("released polyphonic graph retained a stuck note: energy %g", lateEnergy)
				}
				if stableMemory == 0 || module.Memory().Size() != stableMemory {
					t.Fatalf("WASM memory grew after chord/steal warmup: %d to %d", stableMemory, module.Memory().Size())
				}
				compareMusicalMessages(t, wasmEvents, nativeEvents)
				t.Logf("%d samples, chord/release/steal peak %.6f, native/WASM difference %.9g", frames, peakOutput, peakDifference)
			})
		}
	}
}
