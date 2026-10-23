package cmd

import "testing"

func TestLiveIdentityWireBoundsAndLegacyCompatibility(t *testing.T) {
	for _, record := range []Command{
		{Op: OpNoteOn, Index: 1, Arg0: 60 | 100<<8, Arg1: LiveIdentity},
		{Op: OpNoteOn, Index: 65534, Arg0: 60 | 100<<8, Arg1: LiveIdentity},
		{Op: OpNoteOff, Index: 65535, Arg1: LiveIdentity},
		{Op: OpNoteOff, Index: 60, Arg0: 123},
	} {
		wire, err := EncodeCommand(record, 1)
		if err != nil {
			t.Fatal(record, err)
		}
		got, err := DecodeCommand(wire[:], 1)
		if err != nil || got != record {
			t.Fatal(got, err)
		}
	}
	for _, record := range []Command{
		{Op: OpNoteOn, Arg0: 60 | 100<<8, Arg1: LiveIdentity},
		{Op: OpNoteOn, Index: 65535, Arg0: 60 | 100<<8, Arg1: LiveIdentity},
		{Op: OpNoteOn, Index: 1, Arg0: 60 | 100<<8 | 1<<17, Arg1: LiveIdentity},
		{Op: OpNoteOff, Index: 0, Arg1: LiveIdentity},
		{Op: OpNoteOff, Index: 1, Arg0: 1, Arg1: LiveIdentity},
		{Op: OpNoteOn, Index: 1, Arg0: 60 | 100<<8, Arg1: 2},
	} {
		if _, err := EncodeCommand(record, 1); err == nil {
			t.Fatal("accepted invalid ownership", record)
		}
	}
}
