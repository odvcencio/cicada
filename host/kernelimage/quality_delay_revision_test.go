package kernelimage

import (
	"encoding/binary"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestQualityPolyphonicDelayImageRoundTrip(t *testing.T) {
	source := []byte(`instrument mixed {
  voice poly {
    let amp = adsr(gate, 2ms, 20ms, 0.7, 20ms)
    let tone = svf(pulse(pitch, 0.4), 1200Hz, 0.2)
    out = delay(comb(tone * amp * velocity * 0.05, 1 / pitch, 0.9, 0.3), 5ms)
  }
}
track keys mixed {}
pattern p notes { c4 . e4 . }
scene main { keys=p }
song { main }
`)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		data, err := Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(data, rate, 128)
		if err != nil || !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("mixed graph image: %v", err)
		}
		if _, err := engine.New(decoded); err != nil {
			t.Fatal(err)
		}
		binary.LittleEndian.PutUint16(data[30:32], 0)
		if _, err := Decode(data, rate, 128); err == nil {
			t.Fatal("mixed graph accepted without its delay capability")
		}
	}
}

func TestVersion14QualityOpcodesRemainDecodable(t *testing.T) {
	program := graph.Program{Len: 6, Output: 5}
	program.Nodes[0] = graph.Node{Op: graph.Pitch}
	program.Nodes[1] = graph.Node{Op: graph.Constant, Value: 0.4}
	program.Nodes[2] = graph.Node{Op: graph.Pulse, A: 0, B: 1}
	program.Nodes[3] = graph.Node{Op: graph.Constant, Value: 1200}
	program.Nodes[4] = graph.Node{Op: graph.Constant, Value: 0.2}
	program.Nodes[5] = graph.Node{Op: graph.SVF, A: 2, B: 3, C: 4}
	w := &writer{}
	if err := writeGraph(w, program); err != nil {
		t.Fatal(err)
	}
	// The original version-14 layout used 24 and 25 for pulse and SVF.
	w.data[10+2*8] = 24
	w.data[10+5*8] = 25
	r := &reader{data: w.data}
	decoded, err := readGraph(r, 14, 0)
	if err != nil || !reflect.DeepEqual(program, decoded) {
		t.Fatalf("legacy quality opcodes: %v", err)
	}
	if _, err := graph.NewVoice(decoded, 48000); err != nil {
		t.Fatal(err)
	}
}
