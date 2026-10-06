package graph

import (
	"m31labs.dev/cicada/kernel/amp"
	"testing"
)

func TestNeuralAmpGraphStateCopyResetAllocationFree(t *testing.T) {
	p := Program{Len: 6, Output: 5}
	p.Nodes[0] = Node{Op: Pitch}
	p.Nodes[1] = Node{Op: Saw, A: 0}
	p.Nodes[2] = Node{Op: Constant, Value: 3}
	p.Nodes[3] = Node{Op: NeuralAmp, A: 1, B: 2}
	p.Nodes[4] = Node{Op: Constant, Value: 1}
	p.Nodes[5] = Node{Op: NeuralAmp, A: 3, B: 4}
	for _, rate := range []int{44100, 48000, 96000} {
		a, err := NewVoice(p, rate)
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewVoice(p, rate)
		if err != nil {
			t.Fatal(err)
		}
		a.NoteOn(55, 100, false)
		for i := 0; i < 997; i++ {
			a.Next()
		}
		b.CopyStateFrom(a)
		for i := 0; i < 512; i++ {
			if a.Next() != b.Next() {
				t.Fatal("causal history not copied independently")
			}
		}
		b.Reset()
		for _, model := range b.amps {
			if model != (amp.Model{}) {
				t.Fatal("amp reset mismatch")
			}
		}
		c, err := NewVoice(p, rate)
		if err != nil {
			t.Fatal(err)
		}
		c.CopyStateFrom(a)
		for i := 0; i < 100; i++ {
			if a.Next() != c.Next() {
				t.Fatal("reset aliased copied history")
			}
		}
		a.Reset()
		b.Reset()
		a.NoteOn(55, 100, false)
		b.NoteOn(55, 100, false)
		for i := 0; i < 512; i++ {
			if a.Next() != b.Next() {
				t.Fatal("reset output differs")
			}
		}
		if n := testing.AllocsPerRun(100, func() {
			for i := 0; i < 128; i++ {
				a.Next()
			}
		}); n != 0 {
			t.Fatalf("neural graph allocated %g objects", n)
		}
	}
}
