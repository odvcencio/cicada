//go:build chord_wasm

package main

import (
	"context"
	"encoding/binary"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestChordWASMActualWorkletInitialization(t *testing.T) {
	wasmPath := os.Getenv("CICADA_CHORD_WASM_PATH")
	if wasmPath == "" {
		t.Fatal("set CICADA_CHORD_WASM_PATH to the built reactor")
	}
	wasmPath, err := filepath.Abs(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`tempo 120 key d minor
instrument piano { voice poly { out=sine(pitch)*env(gate,300ms)*0.1 } }
track keys piano {}
pattern a notes { [d4 f4 a4] . . . }
scene verse { keys=a }
song { verse }`))
	if len(ds) != 0 {
		t.Fatalf("source: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("project: %+v", ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "poly.image")
	if err := os.WriteFile(imagePath, image, 0600); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("actual worklet regression requires Node.js")
	}
	output, err := exec.Command(node, "../../host/web/chord_worklet_reactor_test.cjs", wasmPath, imagePath).CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}

func TestChordWASMImageCommandAndNativeParity(t *testing.T) {
	for _, upload := range []bool{false, true} {
		name := "full-image"
		if upload {
			name = "command-upload"
		}
		t.Run(name, func(t *testing.T) { checkChordWASMParity(t, upload) })
	}
}
func checkChordWASMParity(t *testing.T, upload bool) {
	source := `tempo 120 key d minor seed 7
 instrument piano { voice poly { out=sine(pitch)*env(gate,300ms)*0.1 } }
 track keys piano {}
 pattern a notes { [d4 f4 a4]^?70 - . [c4 e4 g4] }
 pattern b notes { [e4 g4 b4] - . . }
 scene verse { keys=a }
 scene chorus { keys=b }
 scene stop { keys=off }
 song { verse chorus stop }`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("source %+v", ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	loadCfg := cfg
	if upload {
		loadCfg.Patterns = make([]engine.PatternBank, cfg.Tracks)
	}
	image, err := kernelimage.Encode(loadCfg)
	if err != nil {
		t.Fatal(err)
	}
	wasmPath := os.Getenv("CICADA_CHORD_WASM_PATH")
	if wasmPath == "" {
		t.Fatal("set CICADA_CHORD_WASM_PATH to the built reactor")
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	module, err := runtime.InstantiateWithConfig(ctx, wasm, wazero.NewModuleConfig().WithStartFunctions())
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		fn := module.ExportedFunction(name)
		if fn == nil {
			t.Fatalf("missing export%s", name)
		}
		values, err := fn.Call(ctx, args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(values) > 0 {
			return values[0]
		}
		return 0
	}
	call("_initialize")
	if call("gosx_audio_capabilities")&1 == 0 {
		t.Fatal("chord capability absent")
	}
	ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if ptr == 0 || !module.Memory().Write(ptr, image) {
		t.Fatal("project allocation failed")
	}
	if call("gosx_audio_init", 48000, 128, 2) != 0 {
		t.Fatal("image14 initialization failed")
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	play := cmd.Command{Op: cmd.OpPlay, Track: 255}
	var commands []cmd.Command
	if upload {
		for track, bank := range cfg.Patterns {
			for slot, pattern := range bank.Slots {
				if pattern.Len > 0 {
					batch, err := kernelimage.PatternCommands(pattern, &loadCfg, uint8(track), uint8(slot), uint32(call("gosx_audio_capabilities")))
					if err != nil {
						t.Fatal(err)
					}
					commands = append(commands, batch...)
				}
			}
		}
	}
	commands = append(commands, play)
	wire := make([]byte, 0, len(commands)*24)
	for _, command := range commands {
		record, err := cmd.EncodeCommand(command, uint8(cfg.Tracks))
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, record[:]...)
	}
	if !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), wire) {
		t.Fatal("command memory failed")
	}
	call("gosx_audio_cmd_commit", uint64(len(commands)))
	native.Push(play)
	outputPtr := uint32(call("gosx_audio_out_ptr"))
	messagePtr := uint32(call("gosx_audio_msg_ptr"))
	var left, right [128]float32
	var maxDelta float64
	for at := 0; at < 288000; at += 128 {
		native.Render(left[:], right[:])
		call("gosx_audio_render", 128)
		bytes, ok := module.Memory().Read(outputPtr, 1024)
		if !ok {
			t.Fatal("output unavailable")
		}
		for i := range left {
			for channel, want := range [...]float32{left[i], right[i]} {
				got := math.Float32frombits(binary.LittleEndian.Uint32(bytes[(channel*128+i)*4:]))
				delta := math.Abs(float64(got - want))
				if delta > maxDelta {
					maxDelta = delta
				}
				if math.IsNaN(delta) || delta > 1e-6 {
					t.Fatalf("native/WASM frame%d channel%d delta%g", at+i, channel, delta)
				}
			}
		}
		count := int(call("gosx_audio_msg_drain"))
		for i := 0; i < count; i++ {
			b, _ := module.Memory().Read(messagePtr+uint32(i*16), 16)
			m, err := cmd.DecodeMessage(b)
			if err != nil || m.Kind == cmd.Fault {
				t.Fatalf("WASM fault %+v %v", m, err)
			}
		}
		var m cmd.Message
		for native.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("native fault %+v", m)
			}
		}
	}
	t.Logf("full chord/chance/tie/scene/stop native-WASM parity: max sample delta%g", maxDelta)
	legacy, _ := cmd.EncodeCommand(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8}, 1)
	module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), legacy[:])
	call("gosx_audio_cmd_commit", 1)
	call("gosx_audio_render", 128)
	found := false
	count := int(call("gosx_audio_msg_drain"))
	for i := 0; i < count; i++ {
		bytes, _ := module.Memory().Read(messagePtr+uint32(i*16), 16)
		message, _ := cmd.DecodeMessage(bytes)
		if message.Kind == cmd.Fault && message.A == cmd.FaultPolyLive {
			found = true
		}
	}
	if !found {
		t.Fatal("WASM legacy poly live command did not emit dedicated fault20")
	}

}
