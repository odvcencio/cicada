package kernelimage

import (
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/voice/drum"
)

func TestInstrumentVibratoImageRoundTrip(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 2, MaxVoices: 2, BPMMilli: 120000}
	cfg.Track[0].Kind, cfg.Track[1].Kind = engine.VoiceAcid, engine.VoiceGraph
	cfg.Track[0].Vibrato = &expression.VibratoConfig{DepthCents: 30, RateHz: 5, DelayMS: 100}
	cfg.Track[1].Graph.Nodes[0] = graph.Node{Op: graph.Pitch}
	cfg.Track[1].Graph.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	cfg.Track[1].Graph.Len, cfg.Track[1].Graph.Output = 2, 1
	cfg.Track[1].Graph.Vibrato = &expression.VibratoConfig{DepthCents: 50, RateHz: 7, DelayMS: 40}
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(data[8:12])&InstrumentVibratoFlag == 0 {
		t.Fatal("vibrato capability absent")
	}
	decoded, err := Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Track[0].Vibrato == nil || *decoded.Track[0].Vibrato != *cfg.Track[0].Vibrato || decoded.Track[1].Vibrato == nil || *decoded.Track[1].Vibrato != *cfg.Track[1].Graph.Vibrato {
		t.Fatalf("vibrato lost: %+v %+v", decoded.Track[0].Vibrato, decoded.Track[1].Vibrato)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
	cfg.Track[1].Vibrato = &expression.VibratoConfig{DepthCents: 10, RateHz: 5}
	if _, err := Encode(cfg); err == nil {
		t.Fatal("conflicting graph and track vibrato accepted")
	}
}

func TestInstrumentVibratoImageRejectsUnsupportedPlacement(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 2, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Track[0].Graph.Vibrato = &expression.VibratoConfig{DepthCents: 20, RateHz: 5}
	if _, err := Encode(cfg); err == nil {
		t.Fatal("graph vibrato on a non-graph track was silently discarded")
	}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceDrums}
	kit := new([drum.LaneCount]engine.KitLaneBinding)
	kit[drum.BD] = engine.KitLaneBinding{Kind: engine.KitLaneGraph, Program: graph.Program{
		Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Gate}},
		Vibrato: &expression.VibratoConfig{DepthCents: 20, RateHz: 5},
	}}
	cfg.Track[0].Kit = kit
	if _, err := Encode(cfg); err == nil {
		t.Fatal("kit vibrato was silently discarded")
	}
}
