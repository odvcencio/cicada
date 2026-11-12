//go:build wasm_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// TestOscillatorBankReuseWASM checks the path a score edit takes in the
// browser: the worklet copies the acid oscillator bank from the running kernel
// instance to a new one and installs it there (host/web/processor.js), so the
// new instance does not rebuild the tables. It runs two instances of the
// shipped core kernel: the first renders an acid score and exports its bank;
// the second installs that bank before it starts, renders the same score, and
// must give the same PCM bits and hand back the same bank bytes.
func TestOscillatorBankReuseWASM(t *testing.T) {
	path := os.Getenv("CICADA_WASM_PATH")
	if path == "" {
		t.Skip("set CICADA_WASM_PATH to the core kernel (build/cicada-kernel.wasm)")
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse([]byte(`cicada 2
tempo 128
seed 11
track bass acid { level = -12dB }
pattern line { c2 c2 c3 . eb2 c2 g2 c3 }
scene main { bass = line }
song { main*2 }
`))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
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
	play, err := cmd.EncodeCommand(cmd.Command{Op: cmd.OpPlay, Track: 0xff}, uint8(cfg.Tracks))
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
	// run starts one instance, installs bank first when it is not nil, renders
	// four seconds and returns the PCM's SHA-256 and the instance's bank image.
	run := func(name string, bank []byte) ([32]byte, []byte) {
		t.Helper()
		module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(name))
		if err != nil {
			t.Fatal(err)
		}
		defer module.Close(ctx)
		call := func(fn string, args ...uint64) uint64 {
			t.Helper()
			results, err := module.ExportedFunction(fn).Call(ctx, args...)
			if err != nil {
				t.Fatalf("%s: %s: %v", name, fn, err)
			}
			if len(results) != 0 {
				return results[0]
			}
			return 0
		}
		read := func(memory api.Memory, ptr uint32, n int) []byte {
			t.Helper()
			data, ok := memory.Read(ptr, uint32(n))
			if !ok {
				t.Fatalf("%s: read of %d bytes at %d is out of bounds", name, n, ptr)
			}
			return bytes.Clone(data)
		}
		call("_initialize")
		if bank != nil {
			ptr := uint32(call("gosx_audio_bank_alloc", uint64(len(bank))))
			if ptr == 0 || !module.Memory().Write(ptr, bank) {
				t.Fatalf("%s: bank upload failed", name)
			}
			if int32(call("gosx_audio_bank_install", rate)) != 0 {
				t.Fatalf("%s: bank install failed", name)
			}
		}
		ptr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
		if ptr == 0 || !module.Memory().Write(ptr, image) {
			t.Fatalf("%s: image upload failed", name)
		}
		if call("gosx_audio_init", rate, block, 2) != 0 {
			t.Fatalf("%s: init failed", name)
		}
		if !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), play[:]) {
			t.Fatalf("%s: play command upload failed", name)
		}
		call("gosx_audio_cmd_commit", 1)
		out := uint32(call("gosx_audio_out_ptr"))
		pcm := sha256.New()
		nonzero := false
		for frames := 0; frames < 4*rate; frames += block {
			call("gosx_audio_render", block)
			data := read(module.Memory(), out, block*2*4)
			nonzero = nonzero || !bytes.Equal(data, make([]byte, len(data)))
			pcm.Write(data)
		}
		if !nonzero {
			t.Fatalf("%s: rendered only silence", name)
		}
		// The pointer call writes the image on first use; the length follows it.
		bankPtr := uint32(call("gosx_audio_bank_image_ptr"))
		size := int(int32(call("gosx_audio_bank_image_len")))
		if size <= 0 || bankPtr == 0 {
			t.Fatalf("%s: no oscillator bank image", name)
		}
		var sum [32]byte
		copy(sum[:], pcm.Sum(nil))
		return sum, read(module.Memory(), bankPtr, size)
	}

	first, bank := run("first", nil)
	second, reused := run("second", bank)
	if first != second {
		t.Fatalf("PCM with the installed bank differs: %x, built %x", second, first)
	}
	if !bytes.Equal(bank, reused) {
		t.Fatalf("the second instance returned a different bank image (%d bytes, exported %d)", len(reused), len(bank))
	}
	t.Logf("PCM %x; bank image %d bytes", first[:8], len(bank))
}
