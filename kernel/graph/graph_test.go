package graph

import "testing"

func TestVoiceRunsWithoutAllocations(t *testing.T) {
	var program Program
	program.Nodes[0] = Node{Op: Pitch}
	program.Nodes[1] = Node{Op: Sine, A: 0}
	program.Len = 2
	program.Output = 1
	voice, err := NewVoice(program, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(69, 127, false)
	allocs := testing.AllocsPerRun(100, func() { voice.Next() })
	if allocs != 0 {
		t.Fatalf("audio callback allocated %.1f objects", allocs)
	}
	var nonzero bool
	for range 128 {
		if voice.Next() != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		t.Fatal("oscillator remained silent")
	}
}

func TestRejectsGraphCycle(t *testing.T) {
	var program Program
	program.Nodes[0] = Node{Op: Sine, A: 0}
	program.Len = 1
	if _, err := NewVoice(program, 48_000); err == nil {
		t.Fatal("self-referential graph was accepted")
	}
}
