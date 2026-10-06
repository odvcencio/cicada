package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

func keysImageConfig() engine.Config {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 8, BPMMilli: 120000}
	cfg.Track[0].Kind, cfg.Track[0].Keys = engine.VoiceKeys, &keyboard.Spec{Patch: 32}
	cfg.Track[0].Keys.Controls[0], cfg.Track[0].Keys.Controls[57], cfg.Track[0].Keys.Controls[127] = .375, -2.25, 8
	return cfg
}

func TestKeysImageSparseRoundtripAndCapability(t *testing.T) {
	cfg := keysImageConfig()
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[30:32]) != kernelimage.KeysCapability {
		t.Fatal("missing keyboard capability")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg.Track, decoded.Track) {
		t.Fatalf("keyboard image roundtrip: %v", err)
	}
	cfg.Track[0].Keys.Controls = [128]float32{}
	blank, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data)-len(blank) != 3*5 {
		t.Fatal("keyboard controls are not sparse")
	}
	for _, capability := range []uint16{0, kernelimage.PianoCapability, 1 << 15} {
		binary.LittleEndian.PutUint16(data[30:32], capability)
		if _, err := kernelimage.Decode(data, 48000, 128); err == nil {
			t.Fatalf("accepted missing/unknown keys capability %d", capability)
		}
	}
}

func TestKeysImageRejectsInvalidPayload(t *testing.T) {
	cfg := keysImageConfig()
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var signature [7]byte
	signature[0], signature[1], signature[2] = 32, 3, 0
	binary.LittleEndian.PutUint32(signature[3:], math.Float32bits(.375))
	offset := bytes.Index(data, signature[:])
	if offset < 0 {
		t.Fatal("keys payload not found")
	}
	mutations := []func([]byte){
		func(b []byte) { b[offset] = 0 },
		func(b []byte) { b[offset] = 33 },
		func(b []byte) { b[offset+1] = 129 },
		func(b []byte) { b[offset+2] = 128 },
		func(b []byte) { b[offset+7] = 0 },
		func(b []byte) { binary.LittleEndian.PutUint32(b[offset+3:], 0) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[offset+3:], math.Float32bits(float32(math.NaN()))) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[offset+3:], math.Float32bits(float32(math.Inf(1)))) },
	}
	for i, mutate := range mutations {
		bad := append([]byte(nil), data...)
		mutate(bad)
		if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
			t.Fatalf("accepted keys payload mutation %d", i)
		}
	}
	for _, patch := range []uint8{0, 33} {
		cfg.Track[0].Keys.Patch = patch
		if _, err := kernelimage.Encode(cfg); err == nil {
			t.Fatalf("encoded invalid patch %d", patch)
		}
	}
	cfg = keysImageConfig()
	cfg.Track[0].Keys.Controls[127] = float32(math.NaN())
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("encoded nonfinite control")
	}
	cfg.Track[0].Keys = nil
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("encoded nil keyboard spec")
	}
}

func TestKeysChordImageAndCommandUpload(t *testing.T) {
	cfg := keysImageConfig()
	cfg.Patterns = make([]engine.PatternBank, 1)
	pattern := &cfg.Patterns[0].Slots[0]
	pattern.Len, pattern.GatePercent = 1, 55
	pattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	pattern.Chords[0] = seq.ChordStep{Count: 3, Notes: [4]uint8{60, 64, 67}}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != kernelimage.ChordImageVersion {
		t.Fatal("keyboard chords missing image version")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg.Patterns, decoded.Patterns) {
		t.Fatalf("keyboard chord roundtrip: %v", err)
	}
	if _, err := kernelimage.PatternCommands(*pattern, &decoded, 0, 0, kernelimage.CapabilityChords); err != nil {
		t.Fatal(err)
	}
	if _, err := kernelimage.PatternCommands(*pattern, &decoded, 0, 0, 0); err == nil {
		t.Fatal("uploaded chord without chord capability")
	}
}

func TestKeysAndExpressionCapabilitiesRemainIndependent(t *testing.T) {
	if kernelimage.ExpressionCapability != 1<<3 || kernelimage.NeuralAmpCapability != 1<<4 || kernelimage.KeysCapability != 1<<5 {
		t.Fatal("published additive capability bits changed")
	}
	cfg := keysImageConfig()
	cfg.Tracks, cfg.MaxVoices = 2, 9
	cfg.Track[1].Kind = engine.VoiceGraph
	cfg.Track[1].Graph = graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pressure}}}
	cfg.Patterns = make([]engine.PatternBank, 2)
	keyPattern := &cfg.Patterns[0].Slots[0]
	keyPattern.Len, keyPattern.GatePercent, keyPattern.Seed = 1, 55, 0x3152454b
	keyPattern.Steps[0], _ = seq.PackStep(seq.Step{Note: 64, Gate: true, Velocity: 109, Ratchet: 1, Probability: 100})
	graphPattern := *keyPattern
	graphPattern.Expression = new([64]seq.Expression)
	graphPattern.Expression[0] = seq.Expression{Set: true, PitchCents: 25, Pressure: .5, Timbre: .75}
	cfg.Patterns[1].Slots[0] = graphPattern
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[30:32]) != kernelimage.KeysCapability|kernelimage.ExpressionCapability {
		t.Fatal("mixed image lost keys or expression capability")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg.Track, decoded.Track) || !reflect.DeepEqual(cfg.Patterns, decoded.Patterns) {
		t.Fatalf("mixed keys and expressive graph image changed: %v", err)
	}
	// Locate the unique keys pattern header and step, then change its blank
	// expression record. A capability needed by another track cannot make
	// unsupported keyboard expression playable.
	var signature [13]byte
	signature[0], signature[4] = keyPattern.Len, keyPattern.GatePercent
	binary.LittleEndian.PutUint32(signature[5:9], keyPattern.Seed)
	binary.LittleEndian.PutUint32(signature[9:], keyPattern.Steps[0])
	offset := bytes.Index(data, signature[:])
	if offset < 0 {
		t.Fatal("unique keys pattern header not found")
	}
	bad := append([]byte(nil), data...)
	bad[offset+len(signature)] = 1
	if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
		t.Fatal("mixed image silently accepted keyboard expression")
	}
	keyPattern.Expression = new([64]seq.Expression)
	if data, err := kernelimage.Encode(cfg); err == nil || data != nil {
		t.Fatal("unsupported keys expression produced a playable image")
	}
}
