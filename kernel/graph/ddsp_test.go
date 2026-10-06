package graph

import "testing"

func TestDDSPStateCopyResetAndAllocationFree(t *testing.T) {
	p := Program{Len: 3, Output: 2}
	p.Nodes[0] = Node{Op: Pitch}
	p.Nodes[1] = Node{Op: Velocity}
	p.Nodes[2] = Node{Op: DDSP, A: 0, B: 1}
	a, err := NewVoice(p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewVoice(p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	a.NoteOn(57, 100, false)
	for range 111 {
		a.Next()
	}
	b.CopyStateFrom(a)
	for range 200 {
		if a.Next() != b.Next() {
			t.Fatal("copied neural state diverged")
		}
	}
	a.Reset()
	for range 100 {
		if a.Next() != 0 {
			t.Fatal("reset voice sounded")
		}
	}
	// Advancing/resetting the copy must not share state with its source.
	a.CopyStateFrom(b)
	a.Reset()
	c, _ := NewVoice(p, 48000)
	c.CopyStateFrom(b)
	for range 200 {
		if b.Next() != c.Next() {
			t.Fatal("state storage shared")
		}
	}
	if n := testing.AllocsPerRun(100, func() {
		a.NoteOn(60, 100, false)
		for range 128 {
			a.Next()
		}
		a.CopyStateFrom(b)
		a.Reset()
	}); n != 0 {
		t.Fatalf("render allocations=%g", n)
	}
}
