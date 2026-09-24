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
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func wasmModulePath() string {
	if path := os.Getenv("CICADA_WASM_PATH"); path != "" {
		return path
	}
	return filepath.Join("..", "..", "build", "cicada-kernel.wasm")
}

func TestAudioWASMProjectImage(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
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
	call("_initialize")
	projectPtr := uint32(call("gosx_audio_project_alloc", uint64(len(image))))
	if projectPtr == 0 || !module.Memory().Write(projectPtr, image) {
		t.Fatal("project image buffer unavailable")
	}
	if call("gosx_audio_track_kind", 0, 1) == 0 {
		t.Fatal("legacy track setup accepted after project image allocation")
	}
	if got := call("gosx_audio_init", 48_000, 128, 2); got != 0 {
		t.Fatalf("project image init failed: %d", got)
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	play := cmd.Command{Op: cmd.OpPlay, Track: 0xff}
	record, err := cmd.EncodeCommand(play, uint8(cfg.Tracks))
	if err != nil || !module.Memory().Write(uint32(call("gosx_audio_cmd_ptr")), record[:]) {
		t.Fatalf("write play command: %v", err)
	}
	call("gosx_audio_cmd_commit", 1)
	if !native.Push(play) {
		t.Fatal("native play command rejected")
	}
	messagePtr := uint32(call("gosx_audio_msg_ptr"))
	drainWASM := func() []cmd.Message {
		t.Helper()
		count := int(call("gosx_audio_msg_drain"))
		messages := make([]cmd.Message, 0, count)
		for i := 0; i < count; i++ {
			data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
			if !ok {
				t.Fatal("message pointer out of bounds")
			}
			message, err := cmd.DecodeMessage(data)
			if err != nil || message.Kind == cmd.Fault {
				t.Fatalf("WASM project message: %+v %v", message, err)
			}
			messages = append(messages, message)
		}
		return messages
	}
	initialMemory := module.Memory().Size()
	output := uint32(call("gosx_audio_out_ptr"))
	var nativeL, nativeR [128]float32
	var wasmMessages, nativeMessages []cmd.Message
	var sounded bool
	for block := 0; block < 1600; block++ {
		call("gosx_audio_render", 128)
		native.Render(nativeL[:], nativeR[:])
		if block == 0 {
			for frame := uint32(0); frame < 256; frame++ {
				sample, ok := module.Memory().ReadFloat32Le(output + frame*4)
				if !ok || math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
					t.Fatalf("invalid first-acid sample %d", frame)
				}
				sounded = sounded || sample != 0
			}
		}
		if block%64 == 63 {
			wasmMessages = append(wasmMessages, drainWASM()...)
			nativeMessages = append(nativeMessages, drainNativeMessages(native)...)
		}
	}
	if !sounded || module.Memory().Size() != initialMemory {
		t.Fatalf("first-acid rendered silence or grew memory: sounded=%v", sounded)
	}
	compareMusicalMessages(t, wasmMessages, nativeMessages)
	var tracks [3]bool
	for _, message := range wasmMessages {
		if message.Kind == cmd.NoteOn && message.Tick == 0 && message.Track < 3 {
			tracks[message.Track] = true
		}
	}
	for track, hit := range tracks {
		if !hit {
			t.Fatalf("first-acid track %d did not sound at tick zero", track)
		}
	}
}

func TestAudioWASMABI(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
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
		{Op: cmd.OpSetPatternLen, Track: 1, Index: 1, Arg1: 3},
	}
	chainNote, err := seq.PackStep(seq.Step{Note: 65, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
	if err != nil {
		t.Fatal(err)
	}
	patternBatch = append(patternBatch,
		cmd.Command{Op: cmd.OpSetPatternLen, Track: 0, Index: 3, Arg1: 4},
		cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: chainNote, Arg1: 4},
	)
	for lane := uint8(0); lane < 6; lane++ {
		drumStep, err := seq.PackStep(seq.Step{Note: lane, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
		if err != nil {
			t.Fatal(err)
		}
		patternBatch = append(patternBatch, cmd.Command{Op: cmd.OpSetStep, Track: 1, Index: 0, Arg0: drumStep, Arg1: 3})
	}
	patternBatch = append(patternBatch, cmd.Command{Op: cmd.OpSelectPattern, Track: 1, Index: 3, Arg0: 2})
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
	var drumHits [6]bool
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
		if decoded.Kind == cmd.NoteOn && decoded.Track == 1 && decoded.Tick == seq.TicksPerBar && decoded.A < 6 {
			drumHits[decoded.A] = true
		}
		if decoded.Kind == cmd.Fault {
			t.Fatalf("pattern playback fault %d", decoded.A)
		}
	}
	if !switched || !sounded {
		t.Fatalf("WASM pattern boundary not observed: switched=%v sounded=%v", switched, sounded)
	}
	for lane, hit := range drumHits {
		if !hit {
			t.Fatalf("WASM drum lane %d did not sound at the shared step", lane)
		}
	}
	compareMusicalMessages(t, patternWASM, drainNativeMessages(native))
	chainBatch := []cmd.Command{
		{Op: cmd.OpSetChain, Track: 0, Index: 0, Arg0: 2 | 2<<8},
		{Op: cmd.OpSetChain, Track: 0, Index: 1, Arg0: 4 | 1<<8},
	}
	for i, c := range chainBatch {
		record, err := cmd.EncodeCommand(c, 2)
		if err != nil || !module.Memory().Write(command+uint32(i*cmd.CommandSize), record[:]) {
			t.Fatalf("write chain command %d: %v", i, err)
		}
	}
	call("gosx_audio_cmd_commit", uint64(len(chainBatch)))
	if !native.PushBatch(chainBatch) {
		t.Fatal("native chain batch rejected")
	}
	for block := 0; block < 200; block++ {
		call("gosx_audio_render", 128)
		native.Render(nativeL[:], nativeR[:])
		for channel := 0; channel < 2; channel++ {
			for frame := 0; frame < 128; frame++ {
				got, ok := module.Memory().ReadFloat32Le(out + uint32((channel*128+frame)*4))
				want := nativeL[frame]
				if channel == 1 {
					want = nativeR[frame]
				}
				if !ok || math.Abs(float64(got-want)) > 1e-6 {
					t.Fatalf("chain native/WASM sample differs at block %d channel %d frame %d: %g / %g", block, channel, frame, want, got)
				}
			}
		}
	}
	if got := module.Memory().Size(); got != memoryBytes {
		t.Fatalf("WASM memory grew during chain playback: %d -> %d", memoryBytes, got)
	}
	count = int(call("gosx_audio_msg_drain"))
	var chainWASM []cmd.Message
	var chainSwitch, chainSound bool
	for i := 0; i < count; i++ {
		data, ok := module.Memory().Read(message+uint32(i*cmd.MessageSize), cmd.MessageSize)
		if !ok {
			t.Fatal("chain message pointer out of bounds")
		}
		decoded, err := cmd.DecodeMessage(data)
		if err != nil || decoded.Kind == cmd.Fault {
			t.Fatalf("chain playback fault: %+v %v", decoded, err)
		}
		chainWASM = append(chainWASM, decoded)
		chainSwitch = chainSwitch || decoded.Kind == cmd.Switched && decoded.Track == 0 && decoded.A == 4 && decoded.Tick == 4800
		chainSound = chainSound || decoded.Kind == cmd.NoteOn && decoded.Track == 0 && decoded.A == 65 && decoded.Tick == 4800
	}
	if !chainSwitch || !chainSound {
		t.Fatalf("WASM chain transition missing: switch=%v note=%v", chainSwitch, chainSound)
	}
	compareMusicalMessages(t, chainWASM, drainNativeMessages(native))
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

func TestAudioWASMArrangement(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
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
	call("_initialize")
	if call("gosx_audio_arrangement_alloc", 2, 2) != 0 {
		t.Fatal("arrangement allocation failed")
	}
	scenePtr := uint32(call("gosx_audio_scene_ptr"))
	songPtr := uint32(call("gosx_audio_song_ptr"))
	if scenePtr == 0 || songPtr == 0 || !module.Memory().WriteByte(scenePtr, 2) || !module.Memory().WriteByte(scenePtr+16, 3) {
		t.Fatal("scene buffer is unavailable")
	}
	if !module.Memory().Write(songPtr, []byte{0, 0, 1, 0, 1, 0, 1, 0}) {
		t.Fatal("song buffer is unavailable")
	}
	if call("gosx_audio_init", 48_000, 128, 2) != 0 {
		t.Fatal("arrangement init failed")
	}
	commandPtr := uint32(call("gosx_audio_cmd_ptr"))
	messagePtr := uint32(call("gosx_audio_msg_ptr"))
	stepA, _ := seq.PackStep(seq.Step{Note: 45, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	stepB, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	batch := []cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: stepA, Arg1: 0},
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: stepB, Arg1: 1},
		{Op: cmd.OpPlay, Track: 0xff},
	}
	for i, command := range batch {
		record, err := cmd.EncodeCommand(command, 1)
		if err != nil || !module.Memory().Write(commandPtr+uint32(i*cmd.CommandSize), record[:]) {
			t.Fatalf("write arrangement command %d: %v", i, err)
		}
	}
	call("gosx_audio_cmd_commit", uint64(len(batch)))
	nativeConfig := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1,
		Scenes: []engine.Scene{
			{Track: [16]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 0}}},
			{Track: [16]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 1}}},
		},
		Song: []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}},
	}
	nativeConfig.Track[0].Kind = engine.VoiceAcid
	native, err := engine.New(nativeConfig)
	if err != nil || !native.PushBatch(batch) {
		t.Fatalf("native arrangement setup: %v", err)
	}
	drainWASM := func() []cmd.Message {
		t.Helper()
		count := int(call("gosx_audio_msg_drain"))
		messages := make([]cmd.Message, 0, count)
		for i := 0; i < count; i++ {
			data, ok := module.Memory().Read(messagePtr+uint32(i*cmd.MessageSize), cmd.MessageSize)
			if !ok {
				t.Fatal("arrangement message pointer out of bounds")
			}
			message, err := cmd.DecodeMessage(data)
			if err != nil || message.Kind == cmd.Fault {
				t.Fatalf("arrangement message: %+v %v", message, err)
			}
			messages = append(messages, message)
		}
		return messages
	}
	memoryBytes := module.Memory().Size()
	var nativeL, nativeR [128]float32
	var wasmMessages, nativeMessages []cmd.Message
	for block := 0; block < 1600; block++ {
		call("gosx_audio_render", 128)
		native.Render(nativeL[:], nativeR[:])
		if block%64 == 63 {
			wasmMessages = append(wasmMessages, drainWASM()...)
			nativeMessages = append(nativeMessages, drainNativeMessages(native)...)
		}
	}
	if module.Memory().Size() != memoryBytes {
		t.Fatal("WASM memory grew during song playback")
	}
	compareMusicalMessages(t, wasmMessages, nativeMessages)
	var switched, sounded bool
	for _, message := range wasmMessages {
		switched = switched || message.Kind == cmd.Switched && message.A == 1 && message.Tick == seq.TicksPerBar
		sounded = sounded || message.Kind == cmd.NoteOn && message.A == 52 && message.Tick == seq.TicksPerBar
	}
	if !switched || !sounded {
		t.Fatalf("song boundary missing: switched=%v sounded=%v", switched, sounded)
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
