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
	for frame := range 32 * rate {
		if frame == 2*rate {
			for _, note := range []uint8{60, 64, 67} {
				i.NoteOff(note)
			}
		}
		i.NextStereo()
		if frame%(rate/2) == 0 {
			if found := subnormaltest.Find(i, 5); len(found) > 0 {
				t.Fatalf("at %.1f s the state holds subnormal numbers: %v", float64(frame)/rate, found)
			}
		}
	}
}
