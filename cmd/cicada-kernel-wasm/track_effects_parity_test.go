//go:build wasm_integration

package main

import (
	"context"
	"fmt"
	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Real reactor parity catches omission of track records or DSP from the core
// and verifies the render callback retains fixed memory across seek/stop/play.
func TestAudioWASMTrackEffectsParity(t *testing.T) {
	path := os.Getenv("CICADA_VOCALS_WASM")
	if path == "" {
		path = filepath.Join("..", "..", "build", "cicada-vocals.wasm")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const source = `cicada 2
fx hp eq { type = highpass frequency = 80Hz q = 0.707 }
fx comp comp { threshold = -18dB ratio = 3 makeup = 0dB }
fx deess deesser { frequency = 6000Hz threshold = -24dB ratio = 4 range = 9dB }
instrument syllable { voice mono { out = (sine(200Hz)*0.2+sine(9000Hz)*0.25)*env(gate,100ms) } }
track lead syllable { insert = hp -> comp -> deess }
track double syllable { insert = hp -> comp -> deess level = -12dB }
pattern line notes { c4 - . c4 . . c4 - }
scene main { lead = line double = line }
song { main*4 }
`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			cfg, err := project.CompileEngine(p, rate, 128)
			if err != nil {
				t.Fatal(err)
			}
			image, err := kernelimage.Encode(cfg)
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
				fn := module.ExportedFunction(name)
				if fn == nil {
					t.Fatal("missing export", name)
				}
				v, err := fn.Call(ctx, args...)
				if err != nil {
					t.Fatal(name, err)
				}
				if len(v) > 0 {
					return v[0]
				}
				return 0
			}
			call("_initialize")
			if call("gosx_audio_capabilities")&uint64(kernelimage.TrackEffectsCapability) == 0 {
				t.Fatal("core lacks track effects")
			}
			ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if ptr == 0 || !module.Memory().Write(ptr, image) {
				t.Fatal("image upload")
			}
			if status := call("gosx_audio_init", uint64(rate), 128, 2); status != 0 {
				t.Fatal("core init", status)
			}
			native, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			push := func(c cmd.Command) {
				t.Helper()
				wire, err := cmd.EncodeCommand(c, uint8(cfg.Tracks))
				if err != nil || !native.Push(c) || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), wire[:]) {
					t.Fatal("command", err)
				}
				call("gosx_audio_cmd_commit", 1)
			}
			out := uint32(call("gosx_audio_out_ptr"))
			messages := uint32(call("gosx_audio_msg_ptr"))
			initialMemory, allocations := module.Memory().Size(), call("gosx_audio_allocation_count")
			var l, r [128]float32
			var maxDelta, energy float64
			for block := 0; block < 450; block++ {
				switch block {
				case 0, 170, 310:
					push(cmd.Command{Op: cmd.OpPlay, Track: 255})
				case 80, 230:
					push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 120})
				case 160, 300:
					push(cmd.Command{Op: cmd.OpStop, Track: 255})
				}
				native.Render(l[:], r[:])
				call("gosx_audio_render", 128)
				for ch, frames := range [2][128]float32{l, r} {
					for i, want := range frames {
						got, ok := module.Memory().ReadFloat32Le(out + uint32((ch*128+i)*4))
						delta := math.Abs(float64(got - want))
						maxDelta = math.Max(maxDelta, delta)
						if !ok || math.IsNaN(delta) || delta > 1e-6 {
							t.Fatalf("block %d channel %d sample %d native=%g wasm=%g delta=%g", block, ch, i, want, got, delta)
						}
						energy += float64(got) * float64(got)
					}
				}
				count := int(call("gosx_audio_msg_drain"))
				for i := 0; i < count; i++ {
					bytes, ok := module.Memory().Read(messages+uint32(i*cmd.MessageSize), cmd.MessageSize)
					if !ok {
						t.Fatal("message memory")
					}
					m, err := cmd.DecodeMessage(bytes)
					if err != nil || m.Kind == cmd.Fault {
						t.Fatal("WASM fault", m, err)
					}
				}
				var m cmd.Message
				for native.Poll(&m) {
					if m.Kind == cmd.Fault {
						t.Fatal("native fault", m)
					}
				}
			}
			if energy < .001 {
				t.Fatal("parity fixture was silent")
			}
			if module.Memory().Size() != initialMemory || call("gosx_audio_allocation_count") != allocations {
				t.Fatal("track DSP callback allocated")
			}
			t.Logf("rate %d max PCM delta %.9g; instance memory %d bytes; callback allocations unchanged", rate, maxDelta, initialMemory)
		})
	}
}

func TestAudioWASMDefaultCoreRejectsTrackEffects(t *testing.T) {
	path := os.Getenv("CICADA_CORE_WASM")
	if path == "" {
		path = filepath.Join("..", "..", "build", "cicada-kernel.wasm")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte("cicada 2\nfx hp eq { type=highpass frequency=80Hz }\ntrack bass acid { insert=hp }\npattern line acid { 1 . }\nscene main { bass=line }\nsong { main }\n"))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
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
		v, err := module.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(v) > 0 {
			return v[0]
		}
		return 0
	}
	call("_initialize")
	if call("gosx_audio_capabilities")&uint64(kernelimage.TrackEffectsCapability) != 0 {
		t.Fatal("default core incorrectly advertises track effects")
	}
	ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if !module.Memory().Write(ptr, image) {
		t.Fatal("image upload")
	}
	if call("gosx_audio_init", 48000, 128, 2) == 0 {
		t.Fatal("default core silently omitted track chain")
	}
}
