//go:build wasm_integration

package main

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/acid"
)

func TestAudioWASMLegacyImageVersions(t *testing.T) {
	const rate, block = 48000, 128
	cfg := engine.Config{SampleRate: rate, MaxBlock: block, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind, cfg.Track[0].Acid = engine.VoiceAcid, acid.DefaultParams()
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
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	for version := uint16(8); version <= 13; version++ {
		legacy := append([]byte(nil), image...)
		// The supported old layouts omit per-send/solo flags before v11,
		// and bus flags before v12. There are no graphs or scenes here.
		if version < 11 {
			legacy = append(legacy[:74], legacy[77:]...)
		}
		if version < 12 {
			legacy = append(legacy[:35], legacy[37:]...)
		}
		binary.LittleEndian.PutUint16(legacy[4:], version)
		module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			t.Fatal(err)
		}
		call := func(name string, args ...uint64) uint64 {
			t.Helper()
			results, err := module.ExportedFunction(name).Call(ctx, args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) == 0 {
				return 0
			}
			return results[0]
		}
		call("_initialize")
		ptr := uint32(call("gosx_audio_project_alloc", uint64(len(legacy))))
		if ptr == 0 || !module.Memory().Write(ptr, legacy) {
			t.Fatal("legacy image upload failed")
		}
		if call("gosx_audio_init", rate, block, 2) != 0 {
			t.Fatalf("WASM rejected legacy image version %d", version)
		}
		commands := []cmd.Command{{Op: cmd.OpPlay, Track: 0xff}, {Op: cmd.OpNoteOn, Track: 0, Arg0: 40 | 100<<8}}
		commandPtr := uint32(call("gosx_audio_cmd_ptr"))
		for i, command := range commands {
			record, err := cmd.EncodeCommand(command, 1)
			if err != nil || !module.Memory().Write(commandPtr+uint32(i*cmd.CommandSize), record[:]) {
				t.Fatal("legacy note upload failed")
			}
		}
		call("gosx_audio_cmd_commit", uint64(len(commands)))
		native, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, command := range commands {
			if !native.Push(command) {
				t.Fatal("native rejected legacy command")
			}
		}
		output := uint32(call("gosx_audio_out_ptr"))
		var left, right [block]float32
		var nonzero bool
		for range 16 {
			call("gosx_audio_render", block)
			native.Render(left[:], right[:])
			for i, value := range left {
				got, ok := module.Memory().ReadFloat32Le(output + uint32(i*4))
				if !ok || math.Abs(float64(got)-float64(value)) > 1e-6 {
					t.Fatalf("legacy v%d PCM mismatch", version)
				}
				nonzero = nonzero || got != 0
			}
		}
		if !nonzero {
			t.Fatalf("legacy v%d rendered silence", version)
		}
		if err := module.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("METRIC: older TinyGo image loading | versions 8..13 | all initialized and rendered native-equivalent PCM")
}
