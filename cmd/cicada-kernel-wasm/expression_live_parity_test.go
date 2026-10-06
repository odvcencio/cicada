//go:build wasm_integration

package main

import (
	"context"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func TestAudioWASMLiveExpressionCommandParity(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000} {
		t.Run(sampleRateName(rate), func(t *testing.T) {
			cfg := engine.Config{SampleRate: rate, MaxBlock: 128, Tracks: 2, MaxVoices: 2, BPMMilli: 120000}
			cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceGraph, Graph: graph.Program{Len: 11, Output: 10, Nodes: [graph.MaxNodes]graph.Node{
				{Op: graph.Pitch}, {Op: graph.Sine, A: 0}, {Op: graph.Pressure}, {Op: graph.Timbre},
				{Op: graph.Add, A: 2, B: 3}, {Op: graph.Multiply, A: 1, B: 4}, {Op: graph.Constant, Value: .1},
				{Op: graph.Multiply, A: 5, B: 6}, {Op: graph.PitchBend}, {Op: graph.Gate}, {Op: graph.Multiply, A: 7, B: 9},
			}}}
			cfg.Track[1] = engine.TrackConfig{Kind: engine.VoiceAcid, GainDB: -18}
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
			if call("gosx_audio_capabilities")&uint64(kernelimage.ExpressionCapability) == 0 {
				t.Fatal("kernel does not advertise expression support")
			}
			imagePtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if imagePtr == 0 || !module.Memory().Write(imagePtr, image) || call("gosx_audio_init", uint64(rate), 128, 2) != 0 {
				t.Fatal("WASM expression initialization failed")
			}
			expression := func(track uint8, id uint16, pitch, pressure, timbre float32) cmd.Command {
				return cmd.Command{Op: cmd.OpNoteExpression, Track: track, Index: id, Arg0: math.Float32bits(pitch), Arg1: math.Float32bits(pressure), Pad: math.Float32bits(timbre)}
			}
			stages := [][]cmd.Command{
				// Controls precede onset in the batch, but apply after it at this tick.
				{expression(0, 1, 123.125, .12345679, .9876543), expression(1, 1, -123.125, .12345679, .9876543), {Op: cmd.OpNoteOn, Track: 0, Index: 1, Arg0: 60 | 100<<8}, {Op: cmd.OpNoteOn, Track: 1, Index: 1, Arg0: 36 | 100<<8}},
				{expression(0, 1, 543.25, .8765432, .23456789), expression(1, 1, -543.25, .8765432, .23456789)},
				{{Op: cmd.OpNoteOn, Track: 0, Index: 2, Arg0: 64 | 100<<8}, {Op: cmd.OpNoteOn, Track: 1, Index: 2, Arg0: 40 | 100<<8}},
				// Old identities cannot bend or release either replacement voice.
				{expression(0, 1, 9600, 1, 1), expression(1, 1, -9600, 1, 1), {Op: cmd.OpNoteOff, Track: 0, Index: 1}, {Op: cmd.OpNoteOff, Track: 1, Index: 1}},
				{expression(0, 2, -234.5, .654321, .3456789), expression(1, 2, 234.5, .654321, .3456789)},
				{expression(0, 2, 0, 0, .5), expression(1, 2, 0, 0, .5)},
				{{Op: cmd.OpNoteOff, Track: 0, Index: 2}, {Op: cmd.OpNoteOff, Track: 1, Index: 2}},
			}
			commandPtr, outputPtr, messagePtr := uint32(call("gosx_audio_cmd_ptr")), uint32(call("gosx_audio_out_ptr")), uint32(call("gosx_audio_msg_ptr"))
			initialAllocations := call("gosx_audio_alloc_bytes")
			var left, right [128]float32
			nonzero := false
			for stage, commands := range stages {
				for i, command := range commands {
					record, err := cmd.EncodeCommand(command, 2)
					if err != nil || command.Op == cmd.OpNoteExpression && record[0] != 25 || !module.Memory().Write(commandPtr+uint32(i*cmd.CommandSize), record[:]) {
						t.Fatalf("write expression command: %v", err)
					}
				}
				if call("gosx_audio_cmd_commit", uint64(len(commands))) != 0 || !native.PushBatch(commands) {
					t.Fatalf("expression command stage %d rejected", stage)
				}
				for block := 0; block < 4; block++ {
					call("gosx_audio_render", 128)
					native.Render(left[:], right[:])
					for channel, samples := range [][]float32{left[:], right[:]} {
						for frame, sample := range samples {
							got, ok := module.Memory().ReadUint32Le(outputPtr + uint32((channel*128+frame)*4))
							if !ok || got != math.Float32bits(sample) {
								t.Fatalf("live expression sample differs: stage=%d block=%d channel=%d frame=%d", stage, block, channel, frame)
							}
							nonzero = nonzero || sample != 0
						}
					}
					var wasmMessages []cmd.Message
					for i, count := 0, int(call("gosx_audio_msg_drain")); i < count; i++ {
						data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
						if !ok {
							t.Fatal("expression message out of bounds")
						}
						message, err := cmd.DecodeMessage(data)
						if err != nil || message.Kind == cmd.Fault {
							t.Fatalf("expression WASM fault: %+v %v", message, err)
						}
						wasmMessages = append(wasmMessages, message)
					}
					if !reflect.DeepEqual(wasmMessages, drainNativeMessages(native)) {
						t.Fatalf("live expression messages differ at stage %d block %d", stage, block)
					}
				}
			}
			if !nonzero || call("gosx_audio_alloc_bytes") != initialAllocations {
				t.Fatal("live expression was silent or allocated in WASM rendering")
			}
			t.Logf("METRIC: live expression ABI25 float32 controls rates=%d sample_mismatches=0 wasm_alloc_bytes=0", rate)
		})
	}
}
