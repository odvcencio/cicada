//go:build wasm_integration

package main

import (
	"context"
	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"os"
	"strconv"
	"testing"
)

func TestAudioWASMScheduleClipsSeekParity(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000} {
		for _, scenario := range []string{"placements", "non-step-loop", "leading-gap", "scene-clips"} {
			t.Run(strconv.Itoa(rate)+"/"+scenario, func(t *testing.T) {
				source := []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern hit { c3 }\narrange { place p bass hit { at = 0ticks length = 100ticks } }\n")
				score, ds := notation.Parse([]byte("cicada 2\ntrack bass acid {}\npattern hits { c3 . c4~ c3 }\narrange { place bass-1 bass hits { at = @1.2.1 length = 1bar } place bass-2 bass hits { at = @3.1.1 length = 1bar } }\n"))
				if scenario == "non-step-loop" {
					score, ds = notation.Parse(source)
				}
				p, ds := project.FromScore(score)
				if p == nil {
					t.Fatal(ds)
				}
				cfg, err := project.CompileEngine(p, rate, 128)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Tracks = 2
				cfg.Patterns = append(cfg.Patterns, engine.PatternBank{})
				cfg.Track[1] = engine.TrackConfig{Kind: engine.VoiceAudio, GainSet: true}
				assetFrames := rate
				if scenario == "scene-clips" {
					assetFrames *= 3
				}
				pcm := make([]float32, assetFrames)
				for i := range pcm {
					pcm[i] = float32(i%137) / 4096
				}
				cfg.Assets = []engine.AudioAsset{{Left: pcm, SampleRate: rate}}
				cfg.Clips = []engine.ClipConfig{{EndFrame: int64(len(pcm))}}
				cfg.Schedule = append([]engine.ScheduleEvent{{Tick: 0, EndTick: 720, Kind: engine.ScheduleClip, Track: 1, ID: 3}, {Tick: 720, EndTick: 720, Kind: engine.ScheduleClipEnd, Track: 1, ID: 3}}, cfg.Schedule...)
				// Sort through the same stable chronological ordering as the compiler.
				for i := 1; i < len(cfg.Schedule); i++ {
					v := cfg.Schedule[i]
					j := i
					for j > 0 && cfg.Schedule[j-1].Tick > v.Tick {
						cfg.Schedule[j] = cfg.Schedule[j-1]
						j--
					}
					cfg.Schedule[j] = v
				}
				if scenario == "non-step-loop" {
					cfg.Schedule, err = project.CompileSchedule(p)
					if err != nil {
						t.Fatal(err)
					}
				} else if scenario == "leading-gap" {
					cfg.Schedule = []engine.ScheduleEvent{{Tick: 240, EndTick: 960, Kind: engine.ScheduleClip, Track: 1, ID: 3}, {Tick: 960, EndTick: 960, Kind: engine.ScheduleClipEnd, Track: 1, ID: 3}}
				} else if scenario == "scene-clips" {
					cfg.Schedule = nil
					cfg.BPMMilli = 300000
					cfg.Scenes = make([]engine.Scene, 3)
					cfg.Scenes[0].Track[1].Mode = engine.SceneClip
					cfg.Scenes[1].Track[1].Mode = engine.SceneClip
					cfg.Song = []engine.SongEntry{{Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
				}
				cfg.LoopSong = true
				data, err := kernelimage.Encode(cfg)
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				runtime := wazero.NewRuntime(ctx)
				defer runtime.Close(ctx)
				module, err := runtime.Instantiate(ctx, wasm)
				if err != nil {
					t.Fatal(err)
				}
				call := func(name string, args ...uint64) uint64 {
					t.Helper()
					result, err := module.ExportedFunction(name).Call(ctx, args...)
					if err != nil {
						t.Fatal(name, err)
					}
					if len(result) > 0 {
						return result[0]
					}
					return 0
				}
				call("_initialize")
				ptr := uint32(call("gosx_audio_project_alloc", uint64(len(data))))
				if !module.Memory().Write(ptr, data) {
					t.Fatal("image write")
				}
				if result := call("gosx_audio_init", uint64(rate), 128, uint64(cfg.Tracks)); result != 0 {
					t.Fatal("image init", result)
				}
				native, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				push := func(c cmd.Command) {
					t.Helper()
					record, err := cmd.EncodeCommand(c, uint8(cfg.Tracks))
					if err != nil || !native.Push(c) || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) {
						t.Fatal("command write", err)
					}
					call("gosx_audio_cmd_commit", 1)
				}
				push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
				output := uint32(call("gosx_audio_out_ptr"))
				initial := module.Memory().Size()
				var left, right [128]float32
				var message cmd.Message
				for block := 0; block < 2400; block++ {
					if block == 100 || block == 800 {
						push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: 480})
					}
					if block == 1400 {
						push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 2})
					}
					native.Render(left[:], right[:])
					call("gosx_audio_render", 128)
					for channel, data := range [2][128]float32{left, right} {
						for i, want := range data {
							got, ok := module.Memory().ReadFloat32Le(output + uint32((channel*128+i)*4))
							if !ok || got != want {
								t.Fatalf("rate %d block %d channel %d frame %d: wasm=%g native=%g", rate, block, channel, i, got, want)
							}
						}
					}
					for native.Poll(&message) {
						if message.Kind == cmd.Fault {
							t.Fatal(message)
						}
						if scenario == "non-step-loop" && message.Kind == cmd.NoteOn && message.Tick%100 != 0 {
							t.Fatal("rounded placement origin", message)
						}
					}
					count := call("gosx_audio_msg_drain")
					for i := uint64(0); i < count; i++ {
						data, ok := module.Memory().Read(uint32(call("gosx_audio_msg_ptr"))+uint32(i)*cmd.MessageSize, cmd.MessageSize)
						if !ok {
							t.Fatal("message read")
						}
						m, err := cmd.DecodeMessage(data)
						if err != nil || m.Kind == cmd.Fault {
							t.Fatal(m, err)
						}
					}
				}
				if module.Memory().Size() != initial {
					t.Fatal("WASM memory grew while rendering schedule")
				}
			})
		}
	}
}
