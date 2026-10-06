package graph

import (
	"math"
	"testing"
	"unsafe"
)

func TestDelayImpulseAndWrap(t *testing.T) {
	for _, samples := range []float32{1, 1.5, 48, 4095.5, 4096} {
		var state delayState
		ring := make([]float32, MaxDelaySamples)
		for i := 0; i < 2*MaxDelaySamples+8; i++ {
			input := float32(0)
			if i == 0 {
				input = 1
			}
			got := state.linear(ring, input, samples)
			want := float32(0)
			if i == int(samples) {
				want += 1 - (samples - float32(int(samples)))
			}
			if i == int(samples)+1 {
				want += samples - float32(int(samples))
			}
			if got != want {
				t.Fatalf("delay %g sample %d: got %g want %g", samples, i, got, want)
			}
		}
	}
}

func combProgram() Program {
	var p Program
	p.Nodes[0] = Node{Op: Constant, Value: 1}
	p.Nodes[1] = Node{Op: Pitch}
	p.Nodes[2] = Node{Op: Period, A: 0, B: 1}
	p.Nodes[3] = Node{Op: Constant, Value: .995}
	p.Nodes[4] = Node{Op: Constant, Value: .5}
	p.Nodes[5] = Node{Op: Noise}
	p.Nodes[6] = Node{Op: Comb, A: 5, B: 2, C: 3, Value: 4}
	p.Len, p.Output = 7, 6
	return p
}

func TestDelayMemoryBudgetResetAndAllocs(t *testing.T) {
	p := combProgram()
	p.Nodes[7] = Node{Op: Delay, A: 6, B: 2}
	p.Len, p.Output = 8, 7
	v, err := NewVoice(p, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.delayMemory) != 8192 || p.DelaySamples() != 8192 {
		t.Fatal("wrong delay storage")
	}
	v.NoteOn(69, 127, false)
	allocs := testing.AllocsPerRun(100, func() {
		v.NoteOn(72, 127, true)
		for range 128 {
			v.Next()
		}
		v.NoteOff()
		v.Reset()
	})
	if allocs != 0 {
		t.Fatalf("delay render allocated %g objects", allocs)
	}
	for _, x := range v.delayMemory {
		if x != 0 {
			t.Fatal("reset left a delay tail")
		}
	}
	t.Logf("METRIC: graph callback allocations/run | %g; delay rings bytes/voice | %d; total voice storage bytes | %d", allocs, len(v.delayMemory)*4, unsafe.Sizeof(*v)+uintptr(len(v.delays))*unsafe.Sizeof(delayState{})+uintptr(len(v.delayMemory))*4)
	p.Nodes[8] = Node{Op: Delay, A: 7, B: 2}
	p.Len = 9
	if _, err := NewVoice(p, 48_000); err == nil {
		t.Fatal("accepted third ring")
	}
}

func TestCombStableForBoundedDynamicControls(t *testing.T) {
	for _, period := range []float32{4, 4.1, 4.9, 10.1, 100.9, 4096} {
		for _, damping := range []float32{0, .000000001, .5, .9999} {
			var state delayState
			ring := make([]float32, MaxDelaySamples)
			for i := 0; i < 20_000; i++ {
				x := float32(0)
				if i == 0 {
					x = 1
				}
				y := state.comb(ring, x, period, .9999, damping)
				if math.Abs(float64(state.coefficient)) > .5 {
					t.Fatalf("allpass pole too close to unit circle: %g", state.coefficient)
				}
				if !finite(y) || math.Abs(float64(y)) > 2 {
					t.Fatalf("unstable comb: period %g damping %g sample %d value %g", period, damping, i, y)
				}
			}
		}
	}
}

func TestDelayValidation(t *testing.T) {
	for _, value := range []float32{-1, 1.5, 5.5, float32(math.NaN()), float32(math.Inf(1))} {
		p := combProgram()
		p.Nodes[6].Value = value
		if _, err := NewVoice(p, 48_000); err == nil {
			t.Fatalf("accepted damping input %g", value)
		}
	}
	for _, ms := range []float32{0, .001, 100} {
		p := combProgram()
		p.Nodes[2] = Node{Op: Constant, Value: ms}
		if _, err := NewVoice(p, 48_000); err == nil {
			t.Fatalf("accepted time %g", ms)
		}
	}
}
