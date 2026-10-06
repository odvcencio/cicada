//go:build wasm_integration

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// This gate combines the two formerly conflicting formats in one actual
// reactor, including serial sampler handles and scheduled graph cohorts.
func TestAudioWASMUnifiedChordSamplerClipSeekParity(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			source := `cicada 2
tempo 120
instrument piano { voice poly { out=sine(pitch)*env(gate,100ms)*0.1 } }
track keys piano {}
pattern harmony notes { [c4 e4 g4] - . [d4 f4 a4] }
arrange { place first keys harmony { at=0ticks length=960ticks } }
`
			score, ds := notation.Parse([]byte(source))
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			cfg, err := project.CompileEngine(p, rate, 128)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Tracks = 3
			cfg.MaxVoices = 8
			cfg.MasterGainDB = -3
			cfg.MasterBiasL = 0.0001
			cfg.MasterBiasR = -0.0001
			cfg.Patterns = append(cfg.Patterns, engine.PatternBank{}, engine.PatternBank{})
			cfg.Track[1] = engine.TrackConfig{Kind: engine.VoiceAudio, GainSet: true}
			cfg.Track[2] = engine.TrackConfig{Kind: engine.VoiceSample, GainSet: true, Sample: &engine.SamplerConfig{Asset: 0, RootKey: 60, Voices: 2, Loop: true}}
			cfg.Patterns[2].Slots[0] = cfg.Patterns[0].Slots[0]
			clear(cfg.Patterns[2].Slots[0].Chords[:])
			pcm := make([]float32, 512)
			for i := range pcm {
				pcm[i] = float32(math.Sin(2*math.Pi*float64(i)/32) * 0.05)
			}
			cfg.Assets = []engine.AudioAsset{{SampleRate: rate, Left: pcm}}
			cfg.Clips = []engine.ClipConfig{{EndFrame: 512}}
			cfg.Schedule = []engine.ScheduleEvent{
				{Kind: engine.SchedulePattern, Track: 0, Tick: 0, EndTick: 960, ID: 1},
				{Kind: engine.SchedulePattern, Track: 2, Tick: 0, EndTick: 960, ID: 2},
				{Kind: engine.ScheduleClip, Track: 1, Tick: 0, EndTick: 960, ID: 3},
				{Kind: engine.SchedulePatternEnd, Track: 0, Tick: 960, EndTick: 960, ID: 1},
				{Kind: engine.SchedulePatternEnd, Track: 2, Tick: 960, EndTick: 960, ID: 2},
				{Kind: engine.ScheduleClipEnd, Track: 1, Tick: 960, EndTick: 960, ID: 3},
			}
			image, err := kernelimage.Encode(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if binary.LittleEndian.Uint16(image[4:6]) != kernelimage.UnifiedImageVersion {
				t.Fatal("wrong unified image version")
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
				fn := module.ExportedFunction(name)
				if fn == nil {
					t.Fatal("missing export", name)
				}
				out, err := fn.Call(ctx, args...)
				if err != nil {
					t.Fatal(name, err)
				}
				if len(out) > 0 {
					return out[0]
				}
				return 0
			}
			call("_initialize")
			if call("gosx_audio_capabilities")&uint64(kernelimage.CapabilityUnifiedImage) == 0 {
				t.Fatal("unified capability missing")
			}
			ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if ptr == 0 || !module.Memory().Write(ptr, image) {
				t.Fatal("image upload failed")
			}
			if status := call("gosx_audio_init", uint64(rate), 128, 2); status != 0 {
				t.Fatal("init rejected", status)
			}
			native, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			push := func(c cmd.Command) {
				record, err := cmd.EncodeCommand(c, 3)
				if err != nil || !native.Push(c) || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) {
					t.Fatal("command upload", err)
				}
				call("gosx_audio_cmd_commit", 1)
			}
			output := uint32(call("gosx_audio_out_ptr"))
			messages := uint32(call("gosx_audio_msg_ptr"))
			initialMemory := module.Memory().Size()
			var left, right [128]float32
			var maxDelta float64
			for block := 0; block < 1000; block++ {
				switch block {
				case 0, 410, 700:
					push(cmd.Command{Op: cmd.OpPlay, Track: 255})
				case 50, 200, 600:
					push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 240})
				case 400, 690:
					push(cmd.Command{Op: cmd.OpStop, Track: 255})
				}
				native.Render(left[:], right[:])
				call("gosx_audio_render", 128)
				for channel, frames := range [2][128]float32{left, right} {
					for i, want := range frames {
						got, ok := module.Memory().ReadFloat32Le(output + uint32((channel*128+i)*4))
						delta := math.Abs(float64(got - want))
						maxDelta = math.Max(maxDelta, delta)
						if !ok || math.IsNaN(delta) || delta > 1e-6 {
							t.Fatalf("block %d channel %d frame %d native=%g wasm=%g", block, channel, i, want, got)
						}
					}
				}
				var expected, actual []cmd.Message
				var m cmd.Message
				for native.Poll(&m) {
					if m.Kind == cmd.Fault {
						t.Fatal("native fault", m)
					}
					expected = append(expected, m)
				}
				count := int(call("gosx_audio_msg_drain"))
				for i := 0; i < count; i++ {
					b, ok := module.Memory().Read(messages+uint32(i*cmd.MessageSize), cmd.MessageSize)
					if !ok {
						t.Fatal("message memory")
					}
					m, err := cmd.DecodeMessage(b)
					if err != nil || m.Kind == cmd.Fault {
						t.Fatal("WASM fault", m, err)
					}
					actual = append(actual, m)
				}
				if !reflect.DeepEqual(expected, actual) {
					t.Fatalf("native/WASM messages differ at block %d", block)
				}
			}
			if module.Memory().Size() != initialMemory {
				t.Fatal("callback memory growth")
			}
			t.Logf("unified chord+sampler+clip/master gain, 1000 blocks, seek/stop/re-entry; max delta=%g", maxDelta)
		})
	}
}
