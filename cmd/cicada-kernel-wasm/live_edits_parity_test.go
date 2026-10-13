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
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMLiveEditsExport(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
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
	if initialize := module.ExportedFunction("_initialize"); initialize != nil {
		if _, err := initialize.Call(ctx); err != nil {
			t.Fatal(err)
		}
	}
	function := module.ExportedFunction("gosx_audio_live_edits")
	if function == nil {
		t.Fatal("missing WASM export gosx_audio_live_edits")
	}
	result, err := function.Call(ctx)
	if err != nil || len(result) != 1 || result[0] != 1 {
		t.Fatalf("gosx_audio_live_edits returned %v, %v; want 1", result, err)
	}
}

// Steps, scene bindings, a parameter and stereo meters go through the command
// ring of the WASM kernel and the native engine at the same ticks. The audio
// and the status messages must match, and the edits must change the audio.
func TestAudioWASMStepAndBindingPatchParity(t *testing.T) {
	const rate, blockSize, before, after = 48_000, 128, 400, 1_600
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
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
	cfg, err := project.CompileEngine(p, rate, blockSize)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	newNative := func() *engine.Engine {
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	native, control := newNative(), newNative()
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
		result, callErr := function.Call(ctx, args...)
		if callErr != nil {
			t.Fatalf("WASM %s: %v", name, callErr)
		}
		if len(result) == 0 {
			return 0
		}
		return result[0]
	}
	call("_initialize")
	imagePtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if imagePtr == 0 || !module.Memory().Write(imagePtr, image) || call("gosx_audio_init", rate, blockSize, 2) != 0 {
		t.Fatal("WASM engine initialization failed")
	}
	commandPtr, outputPtr, messagePtr := uint32(call("gosx_audio_cmd_ptr")), uint32(call("gosx_audio_out_ptr")), uint32(call("gosx_audio_msg_ptr"))
	commit := func(commands []cmd.Command) {
		t.Helper()
		for i, command := range commands {
			record, err := cmd.EncodeCommand(command, uint8(cfg.Tracks))
			if err != nil || !module.Memory().Write(commandPtr+uint32(i*cmd.CommandSize), record[:]) {
				t.Fatalf("encode command %d (%+v): %v", i, command, err)
			}
		}
		call("gosx_audio_cmd_commit", uint64(len(commands)))
		if !native.PushBatch(commands) {
			t.Fatal("native engine rejected the batch")
		}
	}
	snare, kick := packedDrumStep(t, drum.SD), packedDrumStep(t, drum.BD)
	acid, err := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
	if err != nil {
		t.Fatal(err)
	}
	const editTick = 2 * seq.TicksPerBar
	setup := []cmd.Command{{Op: cmd.OpPlay, Track: 0xff}, {Op: cmd.OpMeterRate, Track: 0xff, Arg0: 4, Arg1: 1}}
	edits := []cmd.Command{
		{Op: cmd.OpSetStep, Track: 1, Index: 1, Arg0: snare, Tick: editTick},
		{Op: cmd.OpSetStep, Track: 1, Index: 5, Arg0: kick, Tick: editTick},
		{Op: cmd.OpSetStep, Track: 0, Index: 3, Arg0: acid, Tick: editTick},
		{Op: cmd.OpSetSceneBinding, Track: 0xff, Index: 0, Arg0: 0 | uint32(engine.SceneSlot)<<8 | 1<<16},
		{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-9), Tick: editTick},
	}
	commit(setup)
	if !control.PushBatch(setup) {
		t.Fatal("control engine rejected setup")
	}
	var nativeL, nativeR, controlL, controlR [blockSize]float32
	var edited, differs, sounded, stereoMeter bool
	for block := 0; block < before+after; block++ {
		if block == before {
			commit(edits)
			edited = true
		}
		call("gosx_audio_render", blockSize)
		native.Render(nativeL[:], nativeR[:])
		control.Render(controlL[:], controlR[:])
		for channel, samples := range [][]float32{nativeL[:], nativeR[:]} {
			controlSamples := [][]float32{controlL[:], controlR[:]}[channel]
			for frame, sample := range samples {
				got, ok := module.Memory().ReadUint32Le(outputPtr + uint32((channel*blockSize+frame)*4))
				if !ok || got != math.Float32bits(sample) {
					t.Fatalf("native/WASM sample differs: block %d channel %d frame %d", block, channel, frame)
				}
				sounded = sounded || sample != 0
				differs = differs || edited && sample != controlSamples[frame]
			}
		}
		wasmMessages := []cmd.Message{}
		for i, count := 0, int(call("gosx_audio_msg_drain")); i < count; i++ {
			data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
			if !ok {
				t.Fatal("message out of bounds")
			}
			message, err := cmd.DecodeMessage(data)
			if err != nil || message.Kind == cmd.Fault {
				t.Fatalf("WASM message %+v: %v", message, err)
			}
			stereoMeter = stereoMeter || message.Kind == cmd.Meter && message.A == 5
			wasmMessages = append(wasmMessages, message)
		}
		nativeMessages := drainNativeMessages(native)
		for _, message := range nativeMessages {
			if message.Kind == cmd.Fault {
				t.Fatalf("native fault %d", message.A)
			}
		}
		drainNativeMessages(control)
		if len(wasmMessages) != len(nativeMessages) {
			t.Fatalf("block %d: %d WASM messages, %d native", block, len(wasmMessages), len(nativeMessages))
		}
		for i := range wasmMessages {
			if wasmMessages[i] != nativeMessages[i] {
				t.Fatalf("block %d message %d differs: %+v / %+v", block, i, nativeMessages[i], wasmMessages[i])
			}
		}
	}
	if !sounded || !differs || !stereoMeter {
		t.Fatalf("run was not informative: sounded=%v edits changed audio=%v stereo meters=%v", sounded, differs, stereoMeter)
	}
}

func packedDrumStep(t *testing.T, lane drum.Lane) uint32 {
	t.Helper()
	v, err := seq.PackStep(seq.Step{Note: uint8(lane), Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
