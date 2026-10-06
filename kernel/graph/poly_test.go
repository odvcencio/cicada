package graph_test

import (
	"fmt"
	"m31labs.dev/cicada/kernel/graph"
	"math"
	"testing"
)

func TestOutOfBandPartialIsSilentWithoutChangingAudibleOscillator(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			program := graph.Program{Len: 6, Output: 5}
			program.Nodes[0] = graph.Node{Op: graph.Constant, Value: float32(rate) * 0.51}
			program.Nodes[1] = graph.Node{Op: graph.Constant, Value: 0.23}
			program.Nodes[2] = graph.Node{Op: graph.Pulse, A: 0, B: 1}
			program.Nodes[3] = graph.Node{Op: graph.Pitch}
			program.Nodes[4] = graph.Node{Op: graph.Sine, A: 3}
			program.Nodes[5] = graph.Node{Op: graph.Add, A: 2, B: 4}
			voice, err := graph.NewVoice(program, rate)
			if err != nil {
				t.Fatal(err)
			}
			program.Nodes[0].Value = 0
			reference, err := graph.NewVoice(program, rate)
			if err != nil {
				t.Fatal(err)
			}
			voice.NoteOn(69, 127, false)
			reference.NoteOn(69, 127, false)
			var energy float64
			for range 8192 {
				got, want := voice.Next(), reference.Next()
				if got != want {
					t.Fatal("out-of-band partial changed the audible oscillator")
				}
				energy += float64(got) * float64(got)
			}
			if energy < 100 {
				t.Fatal("silencing the partial also silenced the valid tone")
			}
		})
	}
}

func TestStolenRepeatedPitchConsumesItsOwnNoteOff(t *testing.T) {
	// Gate output isolates voice ownership from envelopes and oscillator phase.
	program := graph.Program{Len: 3, Output: 2}
	program.Nodes[0] = graph.Node{Op: graph.Gate}
	program.Nodes[1] = graph.Node{Op: graph.Constant, Value: 0.05}
	program.Nodes[2] = graph.Node{Op: graph.Multiply, A: 0, B: 1}
	p, err := graph.NewPoly(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	for range graph.PolyVoices {
		p.NoteOn(60, 127, false)
	}
	for range 128 {
		p.NextStereo()
	}
	p.NoteOn(64, 127, false) // Steal the oldest C; seven newer Cs remain held.
	p.NoteOffNote(60)        // This belongs to the stolen C, not a newer held C.
	var left float32
	for range 256 {
		left, _ = p.NextStereo()
	}
	if math.Abs(float64(left)-0.4) > 1e-6 {
		t.Fatalf("stolen note-off released a newer voice: %g", left)
	}
	for range graph.PolyVoices - 1 {
		p.NoteOffNote(60)
	}
	for range 256 {
		left, _ = p.NextStereo()
	}
	if math.Abs(float64(left)-0.05) > 1e-6 {
		t.Fatalf("paired C releases lost the independent E: %g", left)
	}
	p.NoteOffNote(64)
	for range 256 {
		left, _ = p.NextStereo()
	}
	if left != 0 {
		t.Fatalf("final note-off retained a voice: %g", left)
	}
}
