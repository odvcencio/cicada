package cmd

import "testing"

func TestDirectorCommandAndMessageABI(t *testing.T) {
	good := []Command{{Op: OpSetState, Track: 255, Index: 63, Arg0: 65535 | 21<<16, Arg1: 9600, Tick: 1<<54 + 3}, {Op: OpTriggerStinger, Track: 2, Index: 15, Arg0: 21, Arg1: 480}}
	for _, c := range good {
		data, err := EncodeCommand(c, 3)
		if err != nil {
			t.Fatal(err)
		}
		copy, err := DecodeCommand(data[:], 3)
		if err != nil || copy != c {
			t.Fatalf("roundtrip: %+v %v", copy, err)
		}
	}
	bad := []Command{{Op: OpSetState, Track: 0}, {Op: OpSetState, Track: 255, Index: 64}, {Op: OpSetState, Track: 255, Arg0: 4 << 16}, {Op: OpTriggerStinger, Track: 255}, {Op: OpTriggerStinger, Track: 2, Index: 16}, {Op: OpTriggerStinger, Track: 2, Arg0: 3}}
	for _, c := range bad {
		if c.Validate(3) == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	for _, kind := range []Kind{StateChanged, StingerStarted, StingerEnded} {
		m := Message{Kind: kind, Track: 2, A: 4, B: 1, Tick: 1<<54 + 3}
		record := EncodeMessage(m)
		copy, err := DecodeMessage(record[:])
		if err != nil || copy != m {
			t.Fatal("message roundtrip")
		}
	}
}
