package graph

import "testing"

// SetConstant changes what a Constant node feeds the graph, on every voice of
// a pool, and leaves other nodes alone.
func TestSetConstantMovesAConstantNodeOnly(t *testing.T) {
	program := Program{Len: 2, Output: 1, Nodes: [MaxNodes]Node{{Op: Constant, Value: 0.25}, {Op: Multiply, A: 0, B: 0}}}
	v, err := NewVoice(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Next(); got != 0.0625 {
		t.Fatalf("before: %g, want 0.0625", got)
	}
	v.SetConstant(0, 0.5)
	v.SetConstant(1, 9) // a Multiply node: ignored
	if got := v.Next(); got != 0.25 {
		t.Fatalf("after: %g, want 0.25", got)
	}
	pool, err := NewPool(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	pool.SetConstant(0, 0.5)
	for i := range pool.slots {
		if pool.slots[i].voice.program.Nodes[0].Value != 0.5 {
			t.Fatalf("pool voice %d kept the old constant", i)
		}
	}
}
