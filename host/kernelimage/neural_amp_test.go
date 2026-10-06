package kernelimage

import (
	"encoding/binary"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"testing"
)

func TestNeuralAmpImageCapability(t *testing.T) {
	program := graph.Program{Len: 3, Output: 2}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: .2}
	program.Nodes[1] = graph.Node{Op: graph.Constant, Value: 2}
	program.Nodes[2] = graph.Node{Op: graph.NeuralAmp, A: 0, B: 1}
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4}
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The required-capability header word is byte offset 30 in version 13.
	const capabilityOffset = 30
	if binary.LittleEndian.Uint16(data[capabilityOffset:])&NeuralAmpCapability == 0 {
		t.Fatal("neural amp capability not encoded")
	}
	decoded, err := Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Track[0].Graph != program {
		t.Fatal("neural amp graph changed on image round trip")
	}
	binary.LittleEndian.PutUint16(data[capabilityOffset:], 0)
	if _, err := Decode(data, 48000, 128); err == nil {
		t.Fatal("accepted neural amp without required capability")
	}
}
