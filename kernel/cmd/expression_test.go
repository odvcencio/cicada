package cmd

import (
	"math"
	"testing"
)

func TestNoteExpressionWirePrecisionAndValidation(t *testing.T) {
	command := Command{Op: OpNoteExpression, Track: 0, Index: 0xabcd, Arg0: math.Float32bits(-123.125), Arg1: math.Float32bits(.12345679), Pad: math.Float32bits(.9876543), Tick: 240}
	encoded, err := EncodeCommand(command, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 24 || encoded[0] != 25 {
		t.Fatal("expression changed the fixed ABI")
	}
	decoded, err := DecodeCommand(encoded[:], 1)
	if err != nil || decoded != command {
		t.Fatalf("expression precision changed: %+v, %v", decoded, err)
	}
	for _, value := range []float32{-9601, 9601, float32(math.NaN()), float32(math.Inf(1))} {
		invalid := command
		invalid.Arg0 = math.Float32bits(value)
		if invalid.Validate(1) == nil {
			t.Fatalf("accepted pitch %g", value)
		}
	}
	for _, value := range []float32{-1, 1.01, float32(math.NaN()), float32(math.Inf(1))} {
		for _, pressure := range []bool{true, false} {
			invalid := command
			if pressure {
				invalid.Arg1 = math.Float32bits(value)
			} else {
				invalid.Pad = math.Float32bits(value)
			}
			if invalid.Validate(1) == nil {
				t.Fatalf("accepted controller %g", value)
			}
		}
	}
	if (Command{Op: Op(26), Track: 0}).Validate(1) == nil {
		t.Fatal("accepted unknown opcode")
	}
	if count := testing.AllocsPerRun(100, func() { EncodeCommand(command, 1); DecodeCommand(encoded[:], 1) }); count != 0 {
		t.Fatalf("expression ABI allocated %v objects", count)
	}
}
