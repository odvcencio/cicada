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
	"m31labs.dev/cicada/project"
)

// A manual scene launch that lands on the exact song-boundary tick must leave
// native and WASM in agreement: the same audio, the same tempo, and the same
// bar placement from the tick_frames export.
func TestSceneTempoManualLaunchOnBoundaryNativeWASMParity(t *testing.T) {
	score, diagnostics, err := project.LoadScore(filepath.Join("..", "..", "examples", "scene-tempo.cicada"), nil)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("score: %v %+v", err, diagnostics)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	const rate, block = 48_000, 128
	cfg, err := project.CompileEngine(p, rate, block)
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
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	module, err := r.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
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
	if !module.Memory().Write(ptr, image) || call("gosx_audio_init", rate, block, 2) != 0 {
		t.Fatal("WASM init failed")
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	push := func(c cmd.Command) {
		t.Helper()
		record, err := cmd.EncodeCommand(c, uint8(cfg.Tracks))
		if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) || !native.Push(c) {
			t.Fatalf("command %v: %v", c.Op, err)
		}
		call("gosx_audio_cmd_commit", 1)
	}
	push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	out := uint32(call("gosx_audio_out_ptr"))
	var left, right [block]float32
	// main*2 is 192000 frames. Launch the "slowest" scene (index 2, 84 BPM) on the
	// bar line where the song starts "slower" (100 BPM), then play four more bars.
	end := int(native.FramesBetweenTicks(0, 6*seq.TicksPerBar))
	for at := 0; at < end; at += block {
		if at == 150_016 {
			push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 2, Arg0: 2})
		}
		native.Render(left[:], right[:])
		call("gosx_audio_render", block)
		for i := range block {
			for channel, value := range [...]float32{left[i], right[i]} {
				got, _ := module.Memory().ReadFloat32Le(out + uint32((channel*block+i)*4))
				if math.Float32bits(got) != math.Float32bits(value) {
					t.Fatalf("native/WASM differ at frame %d channel %d: %v / %v", at+i, channel, value, got)
				}
			}
		}
		var message cmd.Message
		for native.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("native fault %d", message.A)
			}
		}
		call("gosx_audio_msg_drain")
	}
	if index, _ := native.CurrentScene(); index < 0 {
		t.Fatal("no scene is active")
	}
	for bar := int64(1); bar <= 6; bar++ {
		want := native.FramesBetweenTicks(0, bar*seq.TicksPerBar)
		got := int64(math.Float64frombits(call("gosx_audio_tick_frames", math.Float64bits(0), math.Float64bits(float64(bar*seq.TicksPerBar)))))
		if got != want {
			t.Fatalf("bar %d: WASM %d frames, native %d", bar, got, want)
		}
	}
	// Frames per bar match what the transport did, not only what the map says.
	tempo, _ := project.NewTempoMap(rate, int64(p.TempoMilli), project.SongTempoChanges(p))
	if native.FramesBetweenTicks(0, 6*seq.TicksPerBar) != tempo.SampleAtTick(6*seq.TicksPerBar) {
		t.Fatal("the tempo map and the export disagree")
	}
}

// With no scene tempo in the song, a scene outside it that sets 60 BPM and is
// launched by hand moves the transport. The export the AudioWorklet uses to
// place bar boundaries must follow: 288000 frames to bar 2, not 192000, and
// the same in native and WASM.
func TestSceneTempoManualLaunchWithoutChainTickFramesFollowsTheTransport(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Scenes = []engine.Scene{{}, {Settings: []engine.SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 60}}}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 8}}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	module, err := r.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
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
	if !module.Memory().Write(ptr, image) || call("gosx_audio_init", 48_000, 128, 2) != 0 {
		t.Fatal("WASM init failed")
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	push := func(c cmd.Command) {
		t.Helper()
		record, err := cmd.EncodeCommand(c, 1)
		if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) || !native.Push(c) {
			t.Fatalf("command: %v", err)
		}
		call("gosx_audio_cmd_commit", 1)
	}
	frames := func(bar int64) (wasmFrames float64, nativeFrames int64) {
		to := bar * seq.TicksPerBar
		return math.Float64frombits(call("gosx_audio_tick_frames", math.Float64bits(0), math.Float64bits(float64(to)))), native.FramesBetweenTicks(0, to)
	}
	push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	for block := 0; block < 800; block++ {
		if block == 100 {
			push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 1, Arg0: 2})
		}
		var l, rr [128]float32
		native.Render(l[:], rr[:])
		call("gosx_audio_render", 128)
		call("gosx_audio_msg_drain")
		native.Poll(&cmd.Message{})
	}
	for bar := int64(1); bar <= 3; bar++ {
		w, n := frames(bar)
		want := int64(96_000) + (bar-1)*192_000
		if int64(w) != n || n != want {
			t.Fatalf("bar %d: WASM %v native %d want %d", bar, w, n, want)
		}
	}
}

// A looping song whose first and last tempos match each other but not the score
// tempo: the bar placement the AudioWorklet asks for must be the frames the
// native transport takes, across many loops, and the same in WASM.
func TestSceneTempoLoopSeamTickFramesNativeWASMParity(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000, LoopSong: true}
	cfg.Track[0].Kind = engine.VoiceAcid
	set := func(v float32) []engine.SceneSetting {
		return []engine.SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: v}}
	}
	cfg.Scenes = []engine.Scene{{Settings: set(124)}, {Settings: set(90)}, {Settings: set(124)}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	module, err := r.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
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
	if !module.Memory().Write(ptr, image) || call("gosx_audio_init", 48_000, 128, 2) != 0 {
		t.Fatal("WASM init failed")
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var l, rr [1]float32
	reached := map[int64]int64{}
	for frame := int64(1); len(reached) < 15; frame++ {
		native.Render(l[:], rr[:])
		if b := bar2(native); b > 0 {
			if _, seen := reached[b]; !seen {
				reached[b] = frame
			}
		}
	}
	for bar := int64(1); bar <= 15; bar++ {
		got := int64(math.Float64frombits(call("gosx_audio_tick_frames", math.Float64bits(0), math.Float64bits(float64(bar*seq.TicksPerBar)))))
		if got != reached[bar] || got != native.FramesBetweenTicks(0, bar*seq.TicksPerBar) {
			t.Fatalf("bar %d: WASM %d, native prediction %d, the transport took %d", bar, got, native.FramesBetweenTicks(0, bar*seq.TicksPerBar), reached[bar])
		}
	}
}

func bar2(e *engine.Engine) int64 {
	return e.LiveClock().TickAtSample(e.TransportSample()) / seq.TicksPerBar
}
