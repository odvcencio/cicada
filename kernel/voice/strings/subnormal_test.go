package strings

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// After a release the high-pass, low-pass and ensemble filters decay to
// silence; they must reach zero rather than stall in subnormal numbers, which
// tripled the cost of the silence after a chord.
func TestReleaseStateIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	params, err := Patch("string_machine")
	if err != nil {
		t.Fatal(err)
	}
	i, err := New(rate, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range []uint8{60, 64, 67} {
		if err := i.NoteOn(note, 100); err != nil {
			t.Fatal(err)
		}
	}
	hash := subnormaltest.NewHash(rate)
	for frame := range 32 * rate {
		if frame == 2*rate {
			for _, note := range []uint8{60, 64, 67} {
				i.NoteOff(note)
			}
		}
		hash.Add(i.NextStereo())
		// The output filters are swept every flushFrames samples; between
		// sweeps a value may sit in subnormal numbers for those few samples.
		if i.frames%(32*flushFrames) == 0 {
			if found := subnormaltest.Find(i, 5); len(found) > 0 {
				t.Fatalf("after the sweep at %.2f s the state holds subnormal numbers: %v", float64(frame)/rate, found)
			}
		}
	}
	subnormaltest.CheckPin(t, "string machine release", hash, pinnedReleaseHash)
}

// pinnedReleaseHash is the float32 output of TestReleaseStateIsNeverSubnormal
// (2 s held, 30 s released, 48 kHz), produced alike on amd64 and arm64.
const pinnedReleaseHash = 0xe98a9ad610768d38
