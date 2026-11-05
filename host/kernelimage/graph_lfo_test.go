package kernelimage

import (
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func TestGraphLFOImageFlagMatchesGraph(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceGraph
	cfg.Track[0].Graph = graph.Program{Len: 1, Output: 0}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.LFO, Value: 1}
	image, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(image, 48000, 128); err != nil {
		t.Fatal(err)
	}
	missing := append([]byte(nil), image...)
	binary.LittleEndian.PutUint32(missing[8:], binary.LittleEndian.Uint32(missing[8:]) & ^GraphLFOFlag)
	if _, err := Decode(missing, 48000, 128); err == nil {
		t.Fatal("accepted graph LFO without its flag")
	}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.Constant, Value: 1}
	plain, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(plain[8:], binary.LittleEndian.Uint32(plain[8:])|GraphLFOFlag)
	if _, err := Decode(plain, 48000, 128); err == nil {
		t.Fatal("accepted graph LFO flag without an LFO")
	}
}
