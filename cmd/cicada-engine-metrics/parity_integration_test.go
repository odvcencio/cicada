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
	"m31labs.dev/cicada/kernel/seq"
)

func TestSyntheticWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_WASM_PATH")
	if path == "" {
		path = filepath.Join("..", "..", "build", "cicada-kernel.wasm")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scenarios() {
		// All voice/effect/scene/rate/block combinations; maximum tracks also
		// covers simultaneous scene bindings and accumulation order.
		if s.sampler() || (s.tracks != 1 && s.tracks != 16) {
			continue
		}
		t.Run(s.key(), func(t *testing.T) {
			pcmL, pcmR, latency := offlineReference(t, s)
			_, cfg, err := s.score()
			if err != nil {
				t.Fatal(err)
			}
			cfg.LoopSong = true
			if s.scenes {
				cfg.Song = nil
			}
			image, err := kernelimage.Encode(cfg)
			if err != nil {
				t.Fatal(err)
			}
			module, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close(ctx)
			call := func(name string, args ...uint64) uint64 {
				t.Helper()
				result, err := module.ExportedFunction(name).Call(ctx, args...)
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
				t.Fatal("image write failed")
			}
			if call("gosx_audio_init", uint64(s.rate), uint64(s.block), 2) != 0 {
				t.Fatal("WASM initialization failed")
			}
			push := func(c cmd.Command) {
				t.Helper()
				record, err := cmd.EncodeCommand(c, uint8(s.tracks))
				if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) {
					t.Fatal("command write failed")
				}
				call("gosx_audio_cmd_commit", 1)
			}
			push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			if s.scenes {
				push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff})
				push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 1, Tick: seq.TicksPerBar})
			}
			out := uint32(call("gosx_audio_out_ptr"))
			messages := uint32(call("gosx_audio_msg_ptr"))
			var memory uint32
			var difference float64
			for position := 0; position < len(pcmL)+latency; {
				if position == len(pcmL) {
					push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
				}
				frames := min(s.block, len(pcmL)+latency-position)
				if position < len(pcmL) {
					frames = min(frames, len(pcmL)-position)
				}
				call("gosx_audio_render", uint64(frames))
				for i := 0; i < frames; i++ {
					frame := position + i - latency
					if frame < 0 || frame >= len(pcmL) {
						continue
					}
					l, okL := module.Memory().ReadFloat32Le(out + uint32(i*4))
					r, okR := module.Memory().ReadFloat32Le(out + uint32((s.block+i)*4))
					if !okL || !okR || math.IsNaN(float64(l)) || math.IsNaN(float64(r)) {
						t.Fatal("invalid WASM PCM")
					}
					difference = max(difference, math.Abs(float64(l)-float64(pcmL[frame])), math.Abs(float64(r)-float64(pcmR[frame])))
				}
				count := int(call("gosx_audio_msg_drain"))
				for i := 0; i < count; i++ {
					data, ok := module.Memory().Read(messages+uint32(i*cmd.MessageSize), cmd.MessageSize)
					if !ok {
						t.Fatal("message read failed")
					}
					m, err := cmd.DecodeMessage(data)
					if err != nil || m.Kind == cmd.Fault {
						t.Fatalf("WASM fault: %v %v", m, err)
					}
				}
				if position == s.block*10 {
					memory = module.Memory().Size()
				}
				position += frames
			}
			if difference > 1e-6 || module.Memory().Size() != memory {
				t.Fatalf("WASM/offline difference %g; memory %d -> %d", difference, memory, module.Memory().Size())
			}
			t.Logf("METRIC PARITY %s tolerance=1e-6 max_difference=%g memory_growth_bytes=0", s.key(), difference)
		})
	}
}
