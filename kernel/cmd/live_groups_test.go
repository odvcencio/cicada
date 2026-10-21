package cmd

import (
	"math"
	"testing"
)

func TestLiveGroupCommandValidationAndWireRoundTrip(t *testing.T) {
	for _, c := range []Command{
		{Op: OpBindNoteGroup, Index: 7, Arg0: 65534},
		{Op: OpSetLiveGroup, Index: 65534, Arg0: math.Float32bits(-9600), Arg1: math.Float32bits(.125), Pad: math.Float32bits(.9876543)},
	} {
		if err := c.Validate(1); err != nil {
			t.Fatal(err)
		}
		data, err := EncodeCommand(c, 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeCommand(data[:], 1)
		if err != nil || got != c {
			t.Fatal(got, err)
		}
		for _, id := range []uint16{0, 65535} {
			bad := c
			bad.Index = id
			if bad.Validate(1) == nil {
				t.Fatal("reserved group/note identity accepted", bad)
			}
		}
	}
	for _, c := range []Command{
		{Op: OpBindNoteGroup, Index: 1}, {Op: OpBindNoteGroup, Index: 1, Arg0: 65535}, {Op: OpBindNoteGroup, Index: 1, Arg0: 1, Arg1: 1},
		{Op: OpSetLiveGroup, Index: 1, Arg1: math.Float32bits(float32(math.NaN()))}, {Op: OpSetLiveGroup, Index: 1, Pad: math.Float32bits(1.1)},
	} {
		if c.Validate(1) == nil {
			t.Fatal("invalid live shared controls accepted", c)
		}
	}
}
