package midi

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

func TestMIDI2ExpressionPreservesHighResolutionAtABIBoundary(t *testing.T) {
	first := Expression{NoteID: 42, PitchCents: 25.125, Pressure: 0x80000000, Timbre: 0xffffffff}
	a, err := first.Command(0, 1, 240)
	if err != nil {
		t.Fatal(err)
	}
	first.Pressure += 1024 // much less than one 7-bit controller step
	b, err := first.Command(0, 1, 240)
	if err != nil {
		t.Fatal(err)
	}
	if a.Arg1 == b.Arg1 {
		t.Fatal("MIDI 2.0 pressure was reduced to seven-bit resolution")
	}
	wire, err := cmd.EncodeCommand(a, 1)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cmd.DecodeCommand(wire[:], 1)
	if err != nil || decoded != a {
		t.Fatalf("expression ABI round-trip: %+v %v", decoded, err)
	}
	if math.Float32frombits(a.Arg0) != 25.125 || math.Float32frombits(a.Arg1) != .5 || math.Float32frombits(a.Pad) != 1 {
		t.Fatalf("incorrect normalized controls: %+v", a)
	}
}

func TestMIDI2PitchBendEndpointsAndInvalidRanges(t *testing.T) {
	for value, want := range map[uint32]float64{0: -4800, 0x80000000: 0, 0xffffffff: 4800} {
		got, err := PitchBendCents(value, 4800)
		if err != nil || got != want {
			t.Fatalf("bend %x: %g %v, want %g", value, got, err, want)
		}
	}
	for _, value := range []float64{-1, 9601, math.Inf(1), math.NaN()} {
		if _, err := PitchBendCents(0, value); err == nil {
			t.Fatal("accepted invalid pitch sensitivity")
		}
		if _, err := (Expression{NoteID: 1, PitchCents: value * 10000}).Command(0, 1, 0); err == nil {
			t.Fatal("accepted invalid pitch")
		}
	}
}
