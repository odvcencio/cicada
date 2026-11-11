package piano

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// A held chord, its release and the soundboard's ringing in the silence after
// must never leave state in subnormal numbers, which most processors handle
// many times slower than normal ones. Before the flush, the strings and the
// resonators stalled there and a held chord cost 30 times more after 16 s.
func TestDecayingStateIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	p, err := New(rate)
	if err != nil {
		t.Fatal(err)
	}
	chord := []uint8{60, 64, 67, 71}
	for _, note := range chord {
		if err := p.NoteOn(note, 100); err != nil {
			t.Fatal(err)
		}
	}
	for frame := range 46 * rate {
		if frame == 16*rate {
			for _, note := range chord {
				p.NoteOff(note)
			}
		}
		p.NextStereo()
		if frame%(rate/4) == 0 {
			if found := subnormaltest.Find(p, 5); len(found) > 0 {
				t.Fatalf("at %.2f s the state holds subnormal numbers: %v", float64(frame)/rate, found)
			}
		}
	}
}
