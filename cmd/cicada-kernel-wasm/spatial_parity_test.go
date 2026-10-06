//go:build wasm_integration

package main

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func spatialParityConfig(rate int) engine.Config {
	cfg := engine.Config{SampleRate: rate, MaxBlock: 128, Tracks: 2, MaxVoices: 2, BPMMilli: 120000}
	for i := 0; i < 2; i++ {
		cfg.Track[i] = engine.TrackConfig{Kind: engine.VoiceGraph, GainSet: true, GainDB: -6, Graph: graph.Program{Len: 4, Output: 3, Nodes: [graph.MaxNodes]graph.Node{
			{Op: graph.Constant, Value: 173 + float32(i)*91}, {Op: graph.Sine, A: 0}, {Op: graph.Constant, Value: .1}, {Op: graph.Multiply, A: 1, B: 2},
		}}}
	}
	cfg.Track[1].BusSFX = true
	return cfg
}

func spatialParityCommands() []cmd.Command {
	return []cmd.Command{
		{Op: cmd.OpSeek, Track: 255},
		{Op: cmd.OpPlay, Track: 255}, cmd.TrackPosition(0, 1, 0, 0, 0), cmd.TrackPosition(1, -.25, 1, .5, 0),
		cmd.ListenerRotation(math.Pi/2, 0, 0, 7), cmd.ListenerPosition(.125, -.25, .375, 14),
		cmd.ListenerRotation(.731, -.325, 1.51, 21), cmd.TrackPosition(0, -.5, -.75, 1.25, 28),
		cmd.ListenerRotation(-.123, 1.234, -.456, 35), cmd.TrackStereo(1, 42), cmd.TrackPosition(1, 0, 0, 0, 49),
	}
}

func TestAudioWASMSpatialParityAllocationFree(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(strconv.Itoa(rate)+"Hz", func(t *testing.T) {
			cfg := spatialParityConfig(rate)
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
					t.Fatalf("missing %s", name)
				}
				values, err := fn.Call(ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(values) == 0 {
					return 0
				}
				return values[0]
			}
			call("_initialize")
			if call("gosx_audio_capabilities")&uint64(kernelimage.CapabilitySpatial) == 0 {
				t.Fatal("spatial commands not advertised")
			}
			ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if ptr == 0 || !module.Memory().Write(ptr, image) || call("gosx_audio_init", uint64(rate), 128, 2) != 0 {
				t.Fatal("spatial initialization failed")
			}
			beforeBytes, beforeCount, beforeMemory := call("gosx_audio_alloc_bytes"), call("gosx_audio_allocation_count"), module.Memory().Size()
			commands := spatialParityCommands()
			commandPtr, outputPtr, messagePtr := uint32(call("gosx_audio_cmd_ptr")), uint32(call("gosx_audio_out_ptr")), uint32(call("gosx_audio_msg_ptr"))
			for i, c := range commands {
				data, err := cmd.EncodeCommand(c, 2)
				if err != nil || !module.Memory().Write(commandPtr+uint32(i*24), data[:]) {
					t.Fatalf("spatial command: %v", err)
				}
			}
			call("gosx_audio_cmd_commit", uint64(len(commands)))
			if !native.PushBatch(commands) {
				t.Fatal("native spatial commands rejected")
			}
			var left, right [128]float32
			var mismatches int
			nonzero := false
			for block := 0; block < 64; block++ {
				call("gosx_audio_render", 128)
				native.Render(left[:], right[:])
				for channel, samples := range [2][]float32{left[:], right[:]} {
					for frame, sample := range samples {
						got, ok := module.Memory().ReadUint32Le(outputPtr + uint32((channel*128+frame)*4))
						if !ok || got != math.Float32bits(sample) {
							mismatches++
							if mismatches < 3 {
								t.Errorf("spatial PCM differs at block=%d channel=%d frame=%d: WASM=%08x native=%08x", block, channel, frame, got, math.Float32bits(sample))
							}
						}
						nonzero = nonzero || sample != 0
					}
				}
				var messages []cmd.Message
				for i, count := 0, int(call("gosx_audio_msg_drain")); i < count; i++ {
					data, ok := module.Memory().Read(messagePtr+uint32(i*16), 16)
					if !ok {
						t.Fatal("message memory")
					}
					m, err := cmd.DecodeMessage(data)
					if err != nil || m.Kind == cmd.Fault {
						t.Fatalf("spatial WASM fault %+v %v", m, err)
					}
					messages = append(messages, m)
				}
				if !reflect.DeepEqual(messages, drainNativeMessages(native)) {
					t.Fatal("spatial native/WASM messages differ")
				}
			}
			if mismatches != 0 || !nonzero {
				t.Fatalf("spatial parity mismatches=%d nonzero=%t", mismatches, nonzero)
			}
			if call("gosx_audio_alloc_bytes") != beforeBytes || call("gosx_audio_allocation_count") != beforeCount || module.Memory().Size() != beforeMemory {
				t.Fatal("spatial commands or Render allocated or grew memory")
			}
			t.Logf("rate=%d frames=8192 sample_mismatches=0 new_allocations=0 new_alloc_bytes=0 memory_growth=0", rate)
		})
	}
}

func TestAudioWASMSpatialWorkletParity(t *testing.T) {
	cfg := spatialParityConfig(48000)
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	commands := spatialParityCommands()
	if !e.PushBatch(commands) {
		t.Fatal("spatial commands rejected")
	}
	var wire []byte
	for _, c := range commands {
		data, err := cmd.EncodeCommand(c, 2)
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, data[:]...)
	}
	pcm := make([]byte, 8192*8)
	var l, r [128]float32
	for block := 0; block < 64; block++ {
		e.Render(l[:], r[:])
		for i := range l {
			at := (block*128 + i) * 8
			binary.LittleEndian.PutUint32(pcm[at:], math.Float32bits(l[i]))
			binary.LittleEndian.PutUint32(pcm[at+4:], math.Float32bits(r[i]))
		}
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"image": image, "commands": wire, "pcm": pcm} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	process := exec.Command("node", "../../host/web/spatial_worklet_test.cjs", wasmModulePath(), dir)
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("spatial worklet: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}
