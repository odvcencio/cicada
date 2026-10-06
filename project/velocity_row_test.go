package project

import (
	"m31labs.dev/cicada/notation"
	"reflect"
	"testing"
)

func TestVelocityRowHoldsAndRoundTrips(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2 track lead piano {} pattern notes { c3 e3 g3 velocity: 64 . 127 } scene main {lead=notes} song {main}"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	for i, want := range []uint8{64, 64, 127} {
		if p.Patterns[0].Data[i].Velocity != want {
			t.Fatalf("step %d: %+v", i, p.Patterns[0].Data[i])
		}
	}
	if _, err := ToSource(p); err != nil {
		t.Fatal(err)
	}
}

func TestVelocityRowRejectsInvalidValuesAndLengths(t *testing.T) {
	for _, row := range []string{". 64", "0 64", "128 64", "64", "64Hz 100"} {
		score, ds := notation.Parse([]byte("track lead piano {} pattern notes {c3 e3 velocity: " + row + "} scene main {lead=notes} song {main}"))
		if len(ds) > 0 {
			continue
		}
		if p, _ := FromScore(score); p != nil {
			t.Fatalf("row %q accepted", row)
		}
	}
}

func TestVelocityAndExpressionRowsRoundTrip(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2 track lead acid {} pattern notes { step = 1/8t 1 - 5 velocity: 64 . 127 bend: 0ct 200ct 0ct } scene main {lead=notes} song {main}"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	roundTrip, ds := FromScore(parsed)
	if roundTrip == nil || !reflect.DeepEqual(p.Patterns, roundTrip.Patterns) {
		t.Fatalf("velocity/expression roundtrip changed patterns: %+v", ds)
	}
}
