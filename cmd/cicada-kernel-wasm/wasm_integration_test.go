//go:build wasm_integration

package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/cmd"
)

func TestAudioWASMABI(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("..", "..", "build", "cicada-kernel.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		fn := module.ExportedFunction(name)
		if fn == nil {
			t.Fatalf("missing WASM export %s", name)
		}
		result, err := fn.Call(ctx, args...)
		if err != nil {
			t.Fatalf("WASM %s: %v", name, err)
		}
		if len(result) == 0 {
			return 0
		}
		return result[0]
	}
	if module.Memory() == nil {
		t.Fatal("missing WASM memory")
	}
	call("_initialize")
	if got := call("gosx_audio_track_kind", 1, 2); got != 0 {
		t.Fatalf("configure drum track: %d", got)
	}
	if got := call("gosx_audio_init", 48_000, 128, 2); got != 0 {
		t.Fatalf("initialize audio: %d", got)
	}
	out := uint32(call("gosx_audio_out_ptr"))
	command := uint32(call("gosx_audio_cmd_ptr"))
	message := uint32(call("gosx_audio_msg_ptr"))
	if out == 0 || command == 0 || message == 0 || call("gosx_audio_cmd_cap") < 3 {
		t.Fatal("invalid ABI buffers or command capacity")
	}
	batch := []cmd.Command{
		{Op: cmd.OpPlay, Track: 0xff},
		{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 110<<8},
		{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 120<<8},
	}
	for i, c := range batch {
		record, err := cmd.EncodeCommand(c, 2)
		if err != nil || !module.Memory().Write(command+uint32(i*cmd.CommandSize), record[:]) {
			t.Fatalf("write command %d: %v", i, err)
		}
	}
	call("gosx_audio_cmd_commit", uint64(len(batch)))
	var nonzero bool
	for block := 0; block < 16; block++ {
		call("gosx_audio_render", 128)
		for channel := uint32(0); channel < 2; channel++ {
			for frame := uint32(0); frame < 128; frame++ {
				sample, ok := module.Memory().ReadFloat32Le(out + (channel*128+frame)*4)
				if !ok || math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
					t.Fatalf("invalid output at block %d channel %d frame %d", block, channel, frame)
				}
				nonzero = nonzero || sample != 0
			}
		}
	}
	if !nonzero {
		t.Fatal("WASM engine rendered silence after acid and drum notes")
	}
	assertMessages := func(wantFault bool) {
		t.Helper()
		count := int(call("gosx_audio_msg_drain"))
		if count == 0 {
			t.Fatal("WASM engine emitted no messages")
		}
		fault := false
		for i := 0; i < count; i++ {
			data, ok := module.Memory().Read(message+uint32(i*cmd.MessageSize), cmd.MessageSize)
			if !ok {
				t.Fatal("message pointer out of bounds")
			}
			decoded, err := cmd.DecodeMessage(data)
			if err != nil {
				t.Fatal(err)
			}
			fault = fault || decoded.Kind == cmd.Fault
		}
		if fault != wantFault {
			t.Fatalf("fault message = %v, want %v", fault, wantFault)
		}
	}
	assertMessages(false)
	memoryBytes := module.Memory().Size()
	for range 200 {
		call("gosx_audio_render", 128)
	}
	if got := module.Memory().Size(); got != memoryBytes {
		t.Fatalf("WASM memory grew during steady render: %d -> %d bytes", memoryBytes, got)
	}
	bad := [cmd.CommandSize]byte{byte(cmd.OpPlay), 0xff}
	bad[12] = 1 // Invalid reserved padding must fault the whole batch.
	if !module.Memory().Write(command, bad[:]) {
		t.Fatal("command pointer out of bounds")
	}
	call("gosx_audio_cmd_commit", 1)
	call("gosx_audio_render", 128)
	assertMessages(true)
	for frame := uint32(0); frame < 256; frame++ {
		sample, ok := module.Memory().ReadFloat32Le(out + frame*4)
		if !ok || sample != 0 {
			t.Fatalf("faulted engine output at frame %d: %g", frame, sample)
		}
	}
}
