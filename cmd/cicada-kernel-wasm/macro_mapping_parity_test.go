//go:build wasm_integration

package main

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestMappedMacroNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
track bass acid {}
live { macro filter -> bass.cutoff 200Hz..4kHz curve exp smooth 150ms }
pattern p { 1 . 3 . }
scene main { bass = p }
song { main }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
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
	c := &directorClient{name: "macro", wasm: module, ctx: ctx}
	c.call(t, "_initialize")
	ptr := uint32(c.call(t, "gosx_audio_project_alloc", uint64(len(image))))
	if ptr == 0 || !module.Memory().Write(ptr, image) {
		t.Fatal("image memory")
	}
	if c.call(t, "gosx_audio_init", 48000, 128, 2) != 0 {
		t.Fatal("WASM init failed")
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	send := func(command cmd.Command) {
		c.send(t, command)
		if !native.Push(command) {
			t.Fatal("native command rejected")
		}
	}
	for _, command := range project.LiveCommands(p) {
		send(command)
	}
	send(cmd.Command{Op: cmd.OpPlay, Track: 255})
	output := uint32(c.call(t, "gosx_audio_out_ptr"))
	var left, right [128]float32
	for block := 0; block < 64; block++ {
		if block == 4 {
			send(cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(1), Arg1: 7200})
		}
		c.call(t, "gosx_audio_render", 128)
		native.Render(left[:], right[:])
		for channel := 0; channel < 2; channel++ {
			for frame := 0; frame < 128; frame++ {
				got, ok := module.Memory().ReadFloat32Le(output + uint32((channel*128+frame)*4))
				if !ok {
					t.Fatal("output memory")
				}
				want := left[frame]
				if channel == 1 {
					want = right[frame]
				}
				if math.Float32bits(got) != math.Float32bits(want) {
					t.Fatalf("block %d frame %d channel %d: wasm %g native %g", block, frame, channel, got, want)
				}
			}
		}
	}
}
