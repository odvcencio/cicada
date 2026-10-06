package kernelimage

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestQualityChordAndLivePolyImagesCoexist(t *testing.T) {
	source := `instrument chords { voice poly { out = pulse(pitch, 0.4) * adsr(gate, 2ms, 20ms, 0.7, 20ms) * velocity * 0.05 } }
instrument live { voice poly { out = svf(sine(pitch), 1200Hz, 0.2) * env(gate, 20ms) * velocity * 0.05 } }
track harmony chords {}
track lead live {}
pattern chord notes { [c4 e4] . [e4 g4] . }
pattern scalar notes { c4 . e4 . }
scene main { harmony=chord lead=scalar }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Track[0].Kind != engine.VoiceGraph || cfg.Track[0].Polyphony != 4 || cfg.Track[1].Kind != engine.VoiceGraphPoly {
		t.Fatalf("chord and Live voice modes changed: %+v %+v", cfg.Track[0], cfg.Track[1])
	}
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("combined image: %v", err)
	}
	e, err := engine.New(decoded)
	if err != nil || !e.TrackPolyphonic(0) || e.TrackPolyphonic(1) {
		t.Fatalf("chord Live restriction affected the eight-voice mode: %v", err)
	}
	// A chord may disappear during editing without disabling ordinary polyphony.
	score, ds = notation.Parse([]byte(strings.ReplaceAll(source, "[c4 e4] . [e4 g4] .", "c4 . e4 .")))
	p, ds = project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	cfg, err = project.CompileEngine(p, 48000, 128)
	if err != nil || cfg.Track[0].Kind != engine.VoiceGraphPoly {
		t.Fatalf("scalar polyphony did not retain eight voices: %v", err)
	}
}

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
