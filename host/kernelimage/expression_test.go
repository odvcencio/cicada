package kernelimage_test

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func TestExpressionCapabilityImageRoundTripAndCorruption(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.Patterns[0].Slots[0].Expression = new([64]seq.Expression)
	cfg.Patterns[0].Slots[0].Expression[0] = seq.Expression{Set: true, PitchCents: -123.125, Pressure: .12345679, Timbre: .9876543, VibratoRateHz: 5, VibratoDepthCents: 30}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 13 || binary.LittleEndian.Uint16(data[30:32])&kernelimage.ExpressionCapability == 0 {
		t.Fatal("missing additive expression capability")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("expression image precision changed: %v", err)
	}
	withoutCapability := append([]byte(nil), data...)
	withoutCapability[30] &^= byte(kernelimage.ExpressionCapability)
	if _, err := kernelimage.Decode(withoutCapability, 48000, 128); err == nil {
		t.Fatal("expression image accepted without required capability")
	}
	cfg.Patterns[0].Slots[0].Expression[0].PitchCents = float32(math.NaN())
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("encoded nonfinite expression")
	}
}

func TestGraphExpressionInputsRequireCapability(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.Track[0].Graph = graph.Program{Len: 1}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.Timbre}
	cfg.Track[0].Kind = engine.VoiceGraph
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[30:32])&kernelimage.ExpressionCapability == 0 {
		t.Fatal("graph expression missing capability")
	}
	data[30] &^= byte(kernelimage.ExpressionCapability)
	if _, err := kernelimage.Decode(data, 48000, 128); err == nil {
		t.Fatal("graph expression accepted without capability")
	}
}

func TestChordExpressionImageRoundTripAndCommandUploadGuard(t *testing.T) {
	cfg := chordConfig(t)
	pattern := &cfg.Patterns[0].Slots[0]
	pattern.Expression = new([64]seq.Expression)
	pattern.Expression[0] = seq.Expression{Set: true, PitchCents: 123.125, Pressure: .12345679, Timbre: .9876543, VibratoRateHz: 5, VibratoDepthCents: 30}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != kernelimage.ChordImageVersion || binary.LittleEndian.Uint16(data[30:32])&kernelimage.ExpressionCapability == 0 {
		t.Fatal("chords and expression did not advertise their additive image features")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("chord expression image changed: %v", err)
	}
	for _, candidate := range []seq.Pattern{*pattern, {Len: 1, GatePercent: pattern.GatePercent, Seed: pattern.Seed}} {
		if commands, err := kernelimage.PatternCommands(candidate, &cfg, 0, 0, kernelimage.CapabilityChords|uint32(kernelimage.ExpressionCapability)); err == nil || commands != nil {
			t.Fatal("command upload silently discarded or retained step expression")
		}
	}
}
