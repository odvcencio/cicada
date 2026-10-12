package graph

import "testing"

func TestSkippingIdlePoolsPreservesRetriggersAndTails(t *testing.T) {
	program := Program{Len: 6, Output: 5, Nodes: [MaxNodes]Node{
		{Op: Pitch}, {Op: Sine, A: 0}, {Op: Gate}, {Op: Constant, Value: 30},
		{Op: Envelope, A: 2, B: 3}, {Op: Multiply, A: 1, B: 4},
	}}
	a, err := NewPoly(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewPoly(program, 48000)
	pa, err := NewPool(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := NewPool(program, 48000)
	if !a.Idle() || !pa.Idle() {
		t.Fatal("new finite pools should be idle")
	}
	for frame := 0; frame < 24000; frame++ {
		if frame == 5000 || frame == 6000 || frame == 10000 {
			for n := uint8(0); n < PolyVoices+1; n++ {
				a.NoteOn(60+n, 107, false)
				b.NoteOn(60+n, 107, false)
			}
			cohort := Cohort{int64(frame), 1}
			pa.NoteOn([4]uint8{60, 64, 67, 72}, 4, 107, false, cohort)
			pb.NoteOn([4]uint8{60, 64, 67, 72}, 4, 107, false, cohort)
			if a.Idle() || pa.Idle() {
				t.Fatal("held notes reported idle")
			}
		}
		if frame == 7400 || frame == 12000 {
			a.NoteOff()
			b.NoteOff()
			pa.Release(Cohort{5000, 1})
			pb.Release(Cohort{5000, 1})
			pa.Release(Cohort{6000, 1})
			pb.Release(Cohort{6000, 1})
			pa.Release(Cohort{10000, 1})
			pb.Release(Cohort{10000, 1})
			if a.Idle() || pa.Idle() {
				t.Fatal("note-off discarded release or steal tails")
			}
		}
		var actual float32
		if !a.Idle() {
			actual, _ = a.NextStereo()
		}
		expected, _ := b.NextStereo()
		if actual != expected {
			t.Fatalf("poly frame %d: %g != %g", frame, actual, expected)
		}
		actual = 0
		if !pa.Idle() {
			actual = pa.Next()
		}
		if expected = pb.Next(); actual != expected {
			t.Fatalf("pool frame %d: %g != %g", frame, actual, expected)
		}
	}
	if !a.Idle() || !pa.Idle() {
		t.Fatal("completed finite releases did not become idle")
	}
}
