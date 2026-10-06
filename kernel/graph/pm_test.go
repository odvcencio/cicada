package graph

import (
	"math"
	"testing"
)

func pmProgram(index float32) Program {
	return Program{Len: 5, Output: 4, Nodes: [MaxNodes]Node{
		{Op: Pitch}, {Op: Sine, A: 0}, {Op: Constant, Value: index},
		{Op: Sine, A: 0}, {Op: PM, A: 0, B: 1, C: 2},
	}}
}

func TestPMPhaseAndLifecycle(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, index := range []float32{0, 4, -4, 80, math.MaxFloat32} {
			v, err := NewVoice(pmProgram(index), rate)
			if err != nil {
				t.Fatal(err)
			}
			v.NoteOn(69, 127, false)
			for range rate {
				y := v.Next()
				if math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) || y < -1 || y > 1 || v.states[4].phase < 0 || v.states[4].phase >= 1 {
					t.Fatalf("unbounded PM: sample=%g phase=%g", y, v.states[4].phase)
				}
				if index == 0 && y != v.values[3] {
					t.Fatalf("zero index differs from sine: %g vs %g", y, v.values[3])
				}
			}
			phase := v.states[4].phase
			v.NoteOn(72, 127, true)
			if v.states[4].phase != phase {
				t.Fatal("slide reset PM carrier")
			}
			v.NoteOn(69, 127, false)
			if v.states[4].phase != 0 || v.Next() != 0 {
				t.Fatal("retrigger did not reset both operators")
			}
			v.Reset()
			if v.states[4].phase != 0 || v.Next() != 0 {
				t.Fatal("reset did not silence PM")
			}
		}
	}
}

func TestPMOffsetAndInvalidControls(t *testing.T) {
	p := pmProgram(1)
	p.Nodes[1] = Node{Op: Constant, Value: -15}
	v, err := NewVoice(p, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	v.NoteOn(69, 127, false)
	if got, want := v.Next(), float32(math.Sin(-15)); math.Abs(float64(got-want)) > 1e-6 {
		t.Fatalf("offset is not radians: %g vs %g", got, want)
	}
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1))} {
		v.program.Nodes[1].Value = value // simulate a non-finite upstream result
		if got := v.Next(); got != 0 || math.IsNaN(float64(v.states[4].phase)) {
			t.Fatalf("invalid control poisoned PM: %g", got)
		}
	}
	p = pmProgram(1)
	p.Nodes[4].C = 4
	if _, err := NewVoice(p, 48_000); err == nil {
		t.Fatal("PM accepted a cyclic third input")
	}
}

func TestPMVoiceAllocationFree(t *testing.T) {
	v, err := NewVoice(pmProgram(4), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	v.NoteOn(69, 127, false)
	if got := testing.AllocsPerRun(1000, func() { v.Next() }); got != 0 {
		t.Fatalf("PM allocated %g objects/sample", got)
	}
}
