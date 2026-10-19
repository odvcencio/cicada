//go:build chord_wasm

package main

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPerformanceWASMNativeGatesAndActualWorklet(t *testing.T) {
	wasmPath, err := filepath.Abs(os.Getenv("CICADA_CHORD_WASM_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`tempo 120
instrument glass { voice poly { out=sine(pitch)*env(gate,300ms)*velocity*0.1 } }
track keys glass {}
pattern phrase performance length=3840ticks {
 note 60 at=17ticks length=703ticks velocity=82;
 note 64 at=19ticks length=1201ticks velocity=105;
 note 67 at=491ticks length=513ticks velocity=47;
}
scene main { keys=phrase }
song { main*2 }
`))
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
	if call("gosx_audio_capabilities")&uint64(kernelimage.CapabilityPerformance) == 0 {
		t.Fatal("performed-gate capability missing")
	}
	ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if !module.Memory().Write(ptr, image) || call("gosx_audio_init", 48000, 128, 2) != 0 {
		t.Fatal("performance image failed initialization")
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	play := cmd.Command{Op: cmd.OpPlay, Track: 255}
	wire, err := cmd.EncodeCommand(play, 1)
	if err != nil {
		t.Fatal(err)
	}
	module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), wire[:])
	call("gosx_audio_cmd_commit", 1)
	native.Push(play)
	before := call("gosx_audio_allocation_count")
	pcm := make([]byte, 192000*8)
	var left, right [128]float32
	var message cmd.Message
	for frame := 0; frame < 192000; frame += 128 {
		native.Render(left[:], right[:])
		call("gosx_audio_render", 128)
		out, ok := module.Memory().Read(uint32(call("gosx_audio_out_ptr")), 1024)
		if !ok {
			t.Fatal("missing output")
		}
		for i := range left {
			for channel, want := range []float32{left[i], right[i]} {
				got := math.Float32frombits(binary.LittleEndian.Uint32(out[(channel*128+i)*4:]))
				if math.IsNaN(float64(got)) || math.Abs(float64(got-want)) > 1e-6 {
					t.Fatalf("performed PCM mismatch at %d channel %d: %g / %g", frame+i, channel, got, want)
				}
				binary.LittleEndian.PutUint32(pcm[((frame+i)*2+channel)*4:], math.Float32bits(want))
			}
		}
		for native.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal("native fault", message)
			}
		}
		for {
			n := int(call("gosx_audio_msg_drain"))
			if n == 0 {
				break
			}
			messages, _ := module.Memory().Read(uint32(call("gosx_audio_msg_ptr")), uint32(n*16))
			for at := 0; at < n; at++ {
				m, err := cmd.DecodeMessage(messages[at*16 : at*16+16])
				if err != nil || m.Kind == cmd.Fault {
					t.Fatal("WASM fault", m, err)
				}
			}
		}
	}
	if after := call("gosx_audio_allocation_count"); after != before {
		t.Fatalf("WASM rendering allocated: %d -> %d", before, after)
	}
	dir := t.TempDir()
	imagePath, pcmPath := filepath.Join(dir, "performed.image"), filepath.Join(dir, "native.f32")
	if err := os.WriteFile(imagePath, image, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pcmPath, pcm, 0600); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "../../host/web/chord_worklet_reactor_test.cjs", wasmPath, imagePath, pcmPath).CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}
