//go:build wasm_integration

package main

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestEchoHighpassRejectedByPortableKernel(t *testing.T) {
	path := os.Getenv("CICADA_MODULAR_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_MODULAR_WASM_PATH")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte("cicada 2\nfx echo delay {}\ntrack lead acid { send echo = 0.5 }\npattern p { 1 . }\nscene main { lead = p }\nsong { main }\n"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompilePortableEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []kernel.ParamID{kernel.ParamFxDelayHighpass, kernel.ParamFxTapeHighpass} {
		ctx := context.Background()
		runtime := wazero.NewRuntime(ctx)
		module, err := runtime.Instantiate(ctx, wasm)
		if err != nil {
			t.Fatal(err)
		}
		call := func(name string, args ...uint64) uint64 {
			t.Helper()
			result, err := module.ExportedFunction(name).Call(ctx, args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(result) > 0 {
				return result[0]
			}
			return 0
		}
		call("_initialize")
		ptr := uint32(call("gosx_audio_project_alloc", uint64(len(data))))
		if ptr == 0 || !module.Memory().Write(ptr, data) {
			t.Fatal("image upload failed")
		}
		if call("gosx_audio_init", 48000, 128, 2) != 0 {
			t.Fatal("init failed")
		}
		wire, err := cmd.EncodeCommand(cmd.Command{Op: cmd.OpSetParam, Track: 255, Index: uint16(id), Arg0: math.Float32bits(300)}, uint8(cfg.Tracks))
		if err != nil {
			t.Fatal(err)
		}
		if !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), wire[:]) {
			t.Fatal("command upload failed")
		}
		call("gosx_audio_cmd_commit", 1)
		call("gosx_audio_render", 128)
		found := false
		for i, n := uint64(0), call("gosx_audio_msg_drain"); i < n; i++ {
			b, ok := module.Memory().Read(uint32(call("gosx_audio_msg_ptr"))+uint32(i)*cmd.MessageSize, cmd.MessageSize)
			if !ok {
				t.Fatal("message read")
			}
			m, err := cmd.DecodeMessage(b)
			if err != nil {
				t.Fatal(err)
			}
			if m.Kind == cmd.Fault && m.A == engine.FaultParam {
				found = true
			}
		}
		runtime.Close(ctx)
		if !found {
			t.Fatalf("parameter %d did not fault", id)
		}
	}
}
