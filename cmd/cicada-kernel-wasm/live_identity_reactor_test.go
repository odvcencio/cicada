//go:build chord_wasm

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestChordWASMLiveIdentityNativeParity(t *testing.T) {
	wasm, err := os.ReadFile(os.Getenv("CICADA_CHORD_WASM_PATH"))
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
	score, ds := notation.Parse([]byte(`cicada 2
tempo 120
instrument glass { voice poly { out=sine(pitch)*env(gate,50ms)*velocity*(0.03+pressure*0.02+timbre*0.01) } }
track keys glass {}
pattern phrase notes { . . . . }
scene main { keys=phrase }
song { main }`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		for _, voices := range []int{4, 8} {
			t.Run(fmt.Sprintf("%d-%d", rate, voices), func(t *testing.T) {
				cfg, err := project.CompileEngine(p, rate, 256)
				if err != nil {
					t.Fatal(err)
				}
				if voices == 4 {
					cfg.Track[0].Kind = engine.VoiceGraph
					cfg.Track[0].Polyphony = 4
					cfg.MaxVoices = 4
				}
				image, err := kernelimage.Encode(cfg)
				if err != nil {
					t.Fatal(err)
				}
				module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(fmt.Sprintf("%d-%d", rate, voices)).WithStartFunctions())
				if err != nil {
					t.Fatal(err)
				}
				defer module.Close(ctx)
				call := func(name string, args ...uint64) uint64 {
					t.Helper()
					out, err := module.ExportedFunction(name).Call(ctx, args...)
					if err != nil {
						t.Fatal(name, err)
					}
					if len(out) > 0 {
						return out[0]
					}
					return 0
				}
				call("_initialize")
				if call("gosx_audio_capabilities")&uint64(kernelimage.CapabilityLiveIdentity) == 0 {
					t.Fatal("live identity capability missing")
				}
				ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
				bad := append([]byte(nil), image...)
				bad[0] = 0
				module.Memory().Write(ptr, bad)
				if uint32(call("gosx_audio_init", uint64(rate), 256, 2)) != math.MaxUint32 {
					t.Fatal("compact decoder accepted corrupt image")
				}
				module.Memory().Write(ptr, image)
				if call("gosx_audio_init", uint64(rate), 256, 2) != 0 {
					t.Fatal("valid image failed after decoder rejection")
				}
				native, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				expression := func(id uint16, tick int64, cents, pressure float32) cmd.Command {
					return cmd.Command{Op: cmd.OpNoteExpression, Index: id, Tick: tick, Arg0: math.Float32bits(cents), Arg1: math.Float32bits(pressure), Pad: math.Float32bits(.75)}
				}
				commands := []cmd.Command{{Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpNoteOn, Index: 1, Arg0: 60 | 100<<8, Arg1: cmd.LiveIdentity}, expression(1, 0, 25, .2), {Op: cmd.OpNoteOn, Index: 2, Arg0: 60 | 90<<8, Arg1: cmd.LiveIdentity, Tick: 1}, expression(2, 1, -100, .8), expression(1, 80, 212.5, .9), {Op: cmd.OpNoteOff, Index: 1, Arg1: cmd.LiveIdentity, Tick: 120}, expression(1, 200, 9600, 1)}
				for id := uint16(3); id <= 12; id++ {
					tick := int64(240 + id*10)
					commands = append(commands, cmd.Command{Op: cmd.OpNoteOn, Index: id, Arg0: uint32(55+id) | 100<<8, Arg1: cmd.LiveIdentity, Tick: tick}, expression(id, tick, float32(id), .5))
				}
				commands = append(commands, expression(2, 600, 9600, 1), cmd.Command{Op: cmd.OpNoteOff, Index: 2, Arg1: cmd.LiveIdentity, Tick: 610}, cmd.Command{Op: cmd.OpNoteOff, Index: 65535, Arg1: cmd.LiveIdentity, Tick: 700}, cmd.Command{Op: cmd.OpStop, Track: 255, Tick: 1000})
				wire := []byte{}
				for _, command := range commands {
					record, err := cmd.EncodeCommand(command, 1)
					if err != nil {
						t.Fatal(err)
					}
					wire = append(wire, record[:]...)
				}
				module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), wire)
				call("gosx_audio_cmd_commit", uint64(len(commands)))
				if !native.PushBatch(commands) {
					t.Fatal("native rejected live batch")
				}
				allocs := call("gosx_audio_allocation_count")
				clock, _ := seq.NewClock(rate, 120000)
				frames := int(clock.SampleAtTick(1200)) + 8192
				var left, right [256]float32
				outPtr := uint32(call("gosx_audio_out_ptr"))
				msgPtr := uint32(call("gosx_audio_msg_ptr"))
				var maxDelta, energy, tail float64
				for at, block := 0, 0; at < frames; block++ {
					n := min([]int{1, 127, 256}[block%3], frames-at)
					native.Render(left[:n], right[:n])
					call("gosx_audio_render", uint64(n))
					data, _ := module.Memory().Read(outPtr, 2048)
					for i := range n {
						for ch, want := range []float32{left[i], right[i]} {
							got := math.Float32frombits(binary.LittleEndian.Uint32(data[(ch*256+i)*4:]))
							if math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) {
								t.Fatal("nonfinite live output")
							}
							maxDelta = max(maxDelta, math.Abs(float64(got-want)))
							energy += float64(got * got)
							if at > frames-1024 {
								tail += float64(got * got)
							}
						}
					}
					count := int(call("gosx_audio_msg_drain"))
					messages, _ := module.Memory().Read(msgPtr, uint32(count*cmd.MessageSize))
					for i := range count {
						message, err := cmd.DecodeMessage(messages[i*cmd.MessageSize : (i+1)*cmd.MessageSize])
						if err != nil || message.Kind == cmd.Fault {
							t.Fatal("reactor fault", message, err)
						}
					}
					var message cmd.Message
					for native.Poll(&message) {
						if message.Kind == cmd.Fault {
							t.Fatal("native fault", message)
						}
					}
					at += n
				}
				if maxDelta > 1e-5 || energy == 0 || tail != 0 {
					t.Fatal("live parity/silence failed", maxDelta, energy, tail)
				}
				if call("gosx_audio_allocation_count") != allocs {
					t.Fatal("live reactor render allocated")
				}
				t.Logf("live identities, stealing, stale controls and releases: max PCM delta %.8g; no callback allocations", maxDelta)
			})
		}
	}
}
