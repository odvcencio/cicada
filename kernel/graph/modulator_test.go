package graph

import (
	"math"
	"testing"
)

func TestModulatorBindingAndVoiceCopy(t *testing.T) {
	program := Program{Len: 1, Output: 0}
	program.Nodes[0] = Node{Op: Modulator, Value: 1}
	if _, err := NewVoiceFromProgram(&program, 48000); err == nil {
		t.Fatal("unbound source accepted")
	}
	if err := ValidateModulatorIndices(&program, 1); err == nil {
		t.Fatal("missing source accepted")
	}
	var a, b [MaxModulators]float32
	a[1], b[1] = .25, -.75
	first, err := NewVoiceFromProgramWithModulators(&program, 48000, &a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewVoiceFromProgramWithModulators(&program, 48000, &b)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Next(); got != .25 {
		t.Fatalf("got %g", got)
	}
	second.CopyStateFrom(first)
	if got := second.Next(); got != -.75 {
		t.Fatalf("copied source binding: %g", got)
	}
	for _, value := range []float32{-1, 1.5, 32, float32(math.NaN()), float32(math.Inf(1))} {
		program.Nodes[0].Value = value
		if err := ValidateProgram(&program, 48000); err == nil {
			t.Errorf("accepted %g", value)
		}
	}
}
