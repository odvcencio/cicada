package cmd

import "testing"

func TestChordOpcodeAppendedAndRecordRoundTrip(t *testing.T) {
	if OpSetStep != 6 || OpNoteOn != 12 || OpNoteOff != 13 || OpSetPhraseBars != 21 || OpSetChordStep != 22 || CommandSize != 24 {
		t.Fatal("legacy opcode or record ABI changed")
	}
	c, err := ChordStepCommand(0, 3, 5, [4]uint8{62, 65, 69}, 3)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := EncodeCommand(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCommand(wire[:], 1)
	if err != nil || got != c {
		t.Fatal("chord command wire roundtrip")
	}
	for _, slot := range []uint8{16, 255} {
		if _, err := ChordStepCommand(0, slot, 0, [4]uint8{60, 64}, 2); err == nil {
			t.Fatal("invalid slot merged into count bits")
		}
	}
	bad := c
	bad.Arg1 |= 1 << 31
	if err := bad.Validate(1); err == nil {
		t.Fatal("reserved chord bits accepted")
	}
	bad = c
	bad.Arg0 |= 1 << 28
	if err := bad.Validate(1); err == nil {
		t.Fatal("reserved pitch bits accepted")
	}
}

func TestChordBuilderRejectsPitchAliasingBeforePacking(t *testing.T) {
	for _, pitch := range []uint8{128, 255} {
		c, err := ChordStepCommand(0, 0, 0, [4]uint8{pitch, 64}, 2)
		if err == nil || c != (Command{}) {
			t.Fatalf("pitch%d produced aliased command %+v", pitch, c)
		}
	}
}
