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
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
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
	assertMessages := func(wantFault bool) []cmd.Message {
		t.Helper()
		count := int(call("gosx_audio_msg_drain"))
		if count == 0 {
			t.Fatal("WASM engine emitted no messages")
		}
		messages := make([]cmd.Message, 0, count)
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
			messages = append(messages, decoded)
			fault = fault || decoded.Kind == cmd.Fault
		}
		if fault != wantFault {
			t.Fatalf("fault message = %v, want %v", fault, wantFault)
		}
		return messages
	}
	initialWASM := assertMessages(false)
	nativeConfig := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 2, MaxVoices: 32}
	nativeConfig.Track[0].Kind = engine.VoiceAcid
	nativeConfig.Track[1].Kind = engine.VoiceDrums
	native, err := engine.New(nativeConfig)
	if err != nil || !native.PushBatch(batch) {
		t.Fatalf("native parity setup: %v", err)
	}
	var nativeL, nativeR [128]float32
	for range 16 {
		native.Render(nativeL[:], nativeR[:])
	}
	compareMusicalMessages(t, initialWASM, drainNativeMessages(native))
	step, err := seq.PackStep(seq.Step{Note: 60, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
	if err != nil {
		t.Fatal(err)
	}
	patternBatch := []cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 2},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: step, Arg1: 2},
		{Op: cmd.OpSelectPattern, Track: 0, Index: 2, Arg0: 2},
	}
	for i, c := range patternBatch {
		record, err := cmd.EncodeCommand(c, 2)
		if err != nil || !module.Memory().Write(command+uint32(i*cmd.CommandSize), record[:]) {
			t.Fatalf("write pattern command %d: %v", i, err)
		}
	}
	call("gosx_audio_cmd_commit", uint64(len(patternBatch)))
	if !native.PushBatch(patternBatch) {
		t.Fatal("native pattern batch rejected")
	}
	memoryBytes := module.Memory().Size()
	for range 800 {
		call("gosx_audio_render", 128)
		native.Render(nativeL[:], nativeR[:])
	}
	if got := module.Memory().Size(); got != memoryBytes {
		t.Fatalf("WASM memory grew during steady render: %d -> %d bytes", memoryBytes, got)
	}
	count := int(call("gosx_audio_msg_drain"))
	var switched, sounded bool
	patternWASM := make([]cmd.Message, 0, count)
	for i := 0; i < count; i++ {
		data, ok := module.Memory().Read(message+uint32(i*cmd.MessageSize), cmd.MessageSize)
		if !ok {
			t.Fatal("pattern message pointer out of bounds")
		}
		decoded, err := cmd.DecodeMessage(data)
		if err != nil {
			t.Fatal(err)
		}
		patternWASM = append(patternWASM, decoded)
		switched = switched || decoded.Kind == cmd.Switched && decoded.A == 2 && decoded.Tick == seq.TicksPerBar
		sounded = sounded || decoded.Kind == cmd.NoteOn && decoded.A == 60 && decoded.Tick == seq.TicksPerBar
		if decoded.Kind == cmd.Fault {
			t.Fatalf("pattern playback fault %d", decoded.A)
		}
	}
	if !switched || !sounded {
		t.Fatalf("WASM pattern boundary not observed: switched=%v sounded=%v", switched, sounded)
	}
	compareMusicalMessages(t, patternWASM, drainNativeMessages(native))
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

func drainNativeMessages(e *engine.Engine) []cmd.Message {
	var messages []cmd.Message
	var message cmd.Message
	for e.Poll(&message) {
		messages = append(messages, message)
	}
	return messages
}

func compareMusicalMessages(t *testing.T, wasm, native []cmd.Message) {
	t.Helper()
	filter := func(messages []cmd.Message) []cmd.Message {
		var musical []cmd.Message
		for _, message := range messages {
			if message.Kind != cmd.Meter {
				musical = append(musical, message)
			}
		}
		return musical
	}
	wasm, native = filter(wasm), filter(native)
	if len(wasm) != len(native) {
		t.Fatalf("native/WASM event count differs: %d / %d", len(native), len(wasm))
	}
	for i := range wasm {
		if wasm[i] != native[i] {
			t.Fatalf("native/WASM event %d differs: %+v / %+v", i, native[i], wasm[i])
		}
	}
}
