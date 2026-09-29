//go:build wasm_integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMMemoryStableForOneHour(t *testing.T) {
	const rate, blockSize = 48_000, 128
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
	projectPtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if projectPtr == 0 || !module.Memory().Write(projectPtr, image) {
		t.Fatal("project image buffer unavailable")
	}
	if got := call("gosx_audio_init", rate, blockSize, 2); got != 0 {
		t.Fatalf("project init failed: %d", got)
	}
	play, err := cmd.EncodeCommand(cmd.Command{Op: cmd.OpPlay, Track: 0xff}, uint8(cfg.Tracks))
	if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), play[:]) {
		t.Fatalf("write play command: %v", err)
	}
	call("gosx_audio_cmd_commit", 1)
	messagePtr := uint32(call("gosx_audio_msg_ptr"))
	const blocksPerHour = rate * 60 * 60 / blockSize
	const warmupBlocks = rate * 60 / blockSize
	var memoryAfterWarmup uint32
	for block := 0; block < blocksPerHour; block++ {
		call("gosx_audio_render", blockSize)
		if block%64 == 63 {
			count := int(call("gosx_audio_msg_drain"))
			for index := 0; index < count; index++ {
				data, ok := module.Memory().Read(messagePtr+uint32(index*cmd.MessageSize), cmd.MessageSize)
				if !ok {
					t.Fatal("message pointer out of bounds")
				}
				message, err := cmd.DecodeMessage(data)
				if err != nil || message.Kind == cmd.Fault {
					t.Fatalf("WASM fault during one-hour render: %+v %v", message, err)
				}
			}
		}
		if block+1 == warmupBlocks {
			memoryAfterWarmup = module.Memory().Size()
		}
	}
	if memoryAfterWarmup == 0 {
		t.Fatal("WASM memory was not sampled after warmup")
	}
	if got := module.Memory().Size(); got != memoryAfterWarmup {
		t.Fatalf("WASM linear memory grew after 60-second warmup: %d -> %d bytes", memoryAfterWarmup, got)
	}
	t.Logf("one-hour simulated render: %d blocks, linear memory stable at %d bytes after %d warmup blocks", blocksPerHour, memoryAfterWarmup, warmupBlocks)
}
