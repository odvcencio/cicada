package graph

import "testing"

func poolProgram() Program {
	return Program{Len: 5, Output: 4, Nodes: [MaxNodes]Node{{Op: Pitch}, {Op: Sine, A: 0}, {Op: Gate}, {Op: Constant, Value: 300}, {Op: Envelope, A: 2, B: 3}}}
}
func testPool(t *testing.T) *Pool {
	t.Helper()
	p, err := NewPool(poolProgram(), 48000)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPolyPoolStealHandlesResetAndBounds(t *testing.T) {
	p := testPool(t)
	notes := [4]uint8{60, 64, 67, 72}
	old, err := p.NoteOn(notes, 4, 100, false, Cohort{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range old {
		if int(h.Slot) != i {
			t.Fatalf("slot %d: %+v", i, h)
		}
	}
	for i := 0; i < 100; i++ {
		p.Next()
	}
	p.Release(Cohort{1, 1})
	fresh, err := p.NoteOn([4]uint8{62, 65, 69, 74}, 4, 100, false, Cohort{2, 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.ActiveVoices() != 4 {
		t.Fatal("pool exceeded four voices")
	}
	for _, h := range old {
		if p.NoteOff(h) {
			t.Fatal("stale stolen handle released replacement")
		}
	}
	if p.Release(Cohort{1, 1}) {
		t.Fatal("stale cohort released replacement")
	}
	p.Reset()
	for _, h := range fresh {
		if p.NoteOff(h) {
			t.Fatal("pre-reset handle remained valid")
		}
	}
	after, _ := p.NoteOn(notes, 4, 100, false, Cohort{1, 2})
	if after[0].ID <= fresh[3].ID {
		t.Fatal("reset reused handle IDs")
	}
	before := p.serial
	for _, count := range []uint8{0, 5, 255} {
		if _, err := p.NoteOn(notes, count, 100, false, Cohort{3, 2}); err == nil {
			t.Fatalf("invalid count %d accepted", count)
		}
	}
	if p.serial != before {
		t.Fatal("invalid chord changed pool")
	}
	p.serial = ^uint64(0)
	if _, err := p.NoteOn(notes, 4, 100, false, Cohort{4, 2}); err == nil {
		t.Fatal("ID exhaustion accepted")
	}
}
func TestPolyPoolDeterministicResetAndAllocationFree(t *testing.T) {
	p := testPool(t)
	notes := [4]uint8{60, 64, 67}
	p.NoteOn(notes, 3, 100, false, Cohort{1, 1})
	var first [512]float32
	for i := range first {
		first[i] = p.Next()
	}
	p.Reset()
	p.NoteOn(notes, 3, 100, false, Cohort{1, 2})
	for i, want := range first {
		if got := p.Next(); got != want {
			t.Fatalf("reset differs at %d", i)
		}
	}
	id := int64(10)
	if got := testing.AllocsPerRun(100, func() {
		id++
		p.NoteOn(notes, 3, 100, false, Cohort{id, 2})
		p.Next()
		p.Release(Cohort{id, 2})
		p.Next()
	}); got != 0 {
		t.Fatalf("pool callback allocated: %g", got)
	}
	p.ReleaseAll()
	for i := 0; i < 2000; i++ {
		p.Next()
	}
	if p.ActiveVoices() != 0 {
		t.Fatal("release did not retire all bounded slots")
	}
}

func TestPolyPoolNoiseFilterResetAndBoundedTailsAcrossRates(t *testing.T) {
	program := Program{Len: 7, Output: 6, Nodes: [MaxNodes]Node{{Op: Noise}, {Op: Constant, Value: 1000}, {Op: Lowpass, A: 0, B: 1}, {Op: Gate}, {Op: Constant, Value: 30000}, {Op: Envelope, A: 3, B: 4}, {Op: Multiply, A: 2, B: 5}}}
	for _, rate := range []int{44100, 48000, 96000} {
		p, err := NewPool(program, rate)
		if err != nil {
			t.Fatal(err)
		}
		notes := [4]uint8{60, 64, 67, 72}
		p.NoteOn(notes, 4, 100, false, Cohort{1, 1})
		var first [512]float32
		for i := range first {
			first[i] = p.Next()
		}
		p.Reset()
		p.NoteOn(notes, 4, 100, false, Cohort{1, 2})
		for i, want := range first {
			if got := p.Next(); got != want {
				t.Fatalf("noise/filter reset rate%d sample%d", rate, i)
			}
		}
		p.NoteOn([4]uint8{62, 65, 69, 74}, 4, 100, false, Cohort{2, 2})
		if p.ActiveVoices() != 4 {
			t.Fatal("steal added an unbounded tail voice")
		}
		p.ReleaseAll()
		for i := 0; i < rate*30/1000+1; i++ {
			p.Next()
		}
		if p.ActiveVoices() != 0 || p.Next() != 0 {
			t.Fatalf("release/tail was unbounded at rate%d", rate)
		}
	}
}
