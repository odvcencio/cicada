//go:build wasm_integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestStudioMPETakeNativeWASMParity(t *testing.T) {
	source, err := recordedTakeSource([]byte(studioScore), "bass", "pulse", []studioTakeNote{{
		Tick: 0, EndTick: 720, Note: 60, Velocity: 100, NoteID: 42, Channel: 1,
		Expressions: []studioTakeExpression{
			{Tick: 0, PitchCents: 50, Pressure: .5, Timbre: .75},
			{Tick: 30, PitchCents: 70, Pressure: .5, Timbre: .75},
			{Tick: 60, PitchCents: 30, Pressure: .5, Timbre: .75},
			{Tick: 90, PitchCents: 70, Pressure: .5, Timbre: .75},
			{Tick: 110, PitchCents: 30, Pressure: .5, Timbre: .75},
			{Tick: 240, PitchCents: 180, Pressure: .8, Timbre: .9},
			{Tick: 480, PitchCents: 0, Pressure: .2, Timbre: .25},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("CICADA_MPE_TAKE_PATH"); path != "" {
		source, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]byte{[]byte("bend:"), []byte("vibrato:"), []byte("pressure:"), []byte("timbre:")} {
		if !bytes.Contains(source, row) {
			t.Fatalf("MPE take lost %s: %s", row, source)
		}
	}
	document, err := notation.ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	document, err = notation.ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	second, err := notation.Format(document)
	if err != nil || !bytes.Equal(formatted, second) {
		t.Fatal("MPE score does not round-trip through fmt")
	}
	score, diagnostics := notation.Parse(formatted)
	if hasDiagnosticErrors(diagnostics) {
		t.Fatalf("recorded score invalid: %+v", diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(diagnostics) {
		t.Fatalf("recorded project invalid: %+v", diagnostics)
	}
	var vibrato bool
	for _, pattern := range p.Patterns {
		for _, value := range pattern.Expression {
			vibrato = vibrato || value.VibratoDepthCents > 0
		}
	}
	if !vibrato {
		t.Fatal("oscillating MPE take did not record nonzero vibrato depth")
	}
	wasmPath := os.Getenv("CICADA_WASM_PATH")
	if wasmPath == "" {
		wasmPath = filepath.Join("..", "..", "build", "cicada-kernel.wasm")
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			const block = 128
			cfg, err := project.CompileEngine(p, rate, block)
			if err != nil {
				t.Fatal(err)
			}
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
			defer runtime.Close(ctx)
			module, err := runtime.Instantiate(ctx, wasm)
			if err != nil {
				t.Fatal(err)
			}
			call := func(name string, args ...uint64) uint64 {
				t.Helper()
				function := module.ExportedFunction(name)
				if function == nil {
					t.Fatalf("missing WASM export %s", name)
				}
				result, err := function.Call(ctx, args...)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(result) == 0 {
					return 0
				}
				return result[0]
			}
			call("_initialize")
			if call("gosx_audio_capabilities")&uint64(kernelimage.ExpressionCapability) == 0 {
				t.Fatal("kernel expression capability absent")
			}
			ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
			if ptr == 0 || !module.Memory().Write(ptr, image) {
				t.Fatal("image buffer unavailable")
			}
			if call("gosx_audio_init", uint64(rate), block, 2) != 0 {
				t.Fatal("WASM expression image rejected")
			}
			play := cmd.Command{Op: cmd.OpPlay, Track: 255}
			wire, err := cmd.EncodeCommand(play, uint8(cfg.Tracks))
			if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), wire[:]) {
				t.Fatal("play command failed")
			}
			if call("gosx_audio_cmd_commit", 1) != 0 || !native.Push(play) {
				t.Fatal("play rejected")
			}
			output := uint32(call("gosx_audio_out_ptr"))
			allocated := call("gosx_audio_alloc_bytes")
			var left, right [block]float32
			frames := rate * 4 // two bars at 120 BPM, enough for the complete take
			var nonzero bool
			var maximumDifference float64
			for start := 0; start < frames; start += block {
				call("gosx_audio_render", block)
				native.Render(left[:], right[:])
				for channel, samples := range [][]float32{left[:], right[:]} {
					for frame, value := range samples {
						other, ok := module.Memory().ReadFloat32Le(output + uint32((channel*block+frame)*4))
						if !ok || math.IsNaN(float64(other)) || math.IsInf(float64(other), 0) {
							t.Fatal("invalid WASM output")
						}
						nonzero = nonzero || other != 0
						difference := math.Abs(float64(value) - float64(other))
						maximumDifference = math.Max(maximumDifference, difference)
						if math.Float32bits(value) != math.Float32bits(other) {
							t.Fatalf("MPE native/WASM PCM differs at frame%d channel%d: %g/%g", start+frame, channel, value, other)
						}
					}
				}
				call("gosx_audio_msg_drain")
				var message cmd.Message
				for native.Poll(&message) {
				}
			}
			if !nonzero {
				t.Fatal("recorded MPE take rendered silence")
			}
			if count := call("gosx_audio_alloc_bytes") - allocated; count != 0 {
				t.Fatalf("WASM expression render allocated %d bytes", count)
			}
			if allocations := testing.AllocsPerRun(100, func() { native.Render(left[:], right[:]) }); allocations != 0 {
				t.Fatalf("native expression render allocated %g objects", allocations)
			}
			t.Logf("MPE take: rate=%d frames=%d peak_difference=%g native_allocs=0 wasm_alloc_bytes=0 fmt_idempotent=true", rate, frames, maximumDifference)
		})
	}
}
