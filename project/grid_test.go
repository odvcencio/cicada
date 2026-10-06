package project

import (
	"m31labs.dev/cicada/notation"
	"testing"
)

func TestFlexibleGridSourceRoundTrip(t *testing.T) {
	for _, division := range []string{"1/8t", "1/16t", "1/20", "1/8."} {
		source := []byte("cicada 2\ntrack bass acid {}\npattern triplet { step = " + division + " 1 3 5 }\nscene main { bass = triplet }\nsong { main*2 }\n")
		score, ds := notation.Parse(source)
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		p, ds := FromScore(score)
		if p == nil {
			t.Fatal(ds)
		}
		text, err := ToSource(p)
		if err != nil {
			t.Fatal(err)
		}
		again, ds := notation.Parse(text)
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		q, ds := FromScore(again)
		if q == nil || !SemanticEqual(p, q) {
			t.Fatalf("round trip: %s: %v", text, ds)
		}
	}
}

func TestTupletGroupExpandsAtExactTicks(t *testing.T) {
	score, ds := notation.Parse([]byte("track bass acid {} pattern triplet acid { step = 1/8 [1 3 5] - } scene main { bass = triplet } song { main }"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	if p.Patterns[0].StepTicks != 160 || p.Patterns[0].Steps != 6 {
		t.Fatalf("%+v", p.Patterns[0])
	}
	if _, err := ToSource(p); err != nil {
		t.Fatal(err)
	}
}

func TestGridRejectsFractionalTicks(t *testing.T) {
	_, ds := notation.Parse([]byte("track bass acid {} pattern bad { step = 1/28 1 } scene main { bass = bad } song { main }"))
	found := false
	for _, d := range ds {
		found = found || d.Code == "CICADA-GRID"
	}
	if !found {
		t.Fatalf("missing grid diagnostic: %v", ds)
	}
}
