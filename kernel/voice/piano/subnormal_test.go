package piano

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// A held chord, its release and the soundboard's ringing in the silence after
// must never leave state in subnormal numbers, which most processors handle
// many times slower than normal ones. Before the flush, the strings and the
// resonators stalled there and a held chord cost 30 times more after 16 s.
// The output is pinned: no voice golden runs long enough to see a flush.
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
	hash := subnormaltest.NewHash(rate)
	for frame := range 46 * rate {
		if frame == 16*rate {
			for _, note := range chord {
				p.NoteOff(note)
			}
		}
		hash.Add(p.NextStereo())
		// The state is swept every flushFrames samples; between sweeps a value
		// may sit in subnormal numbers for those few samples.
		if p.frames%(64*flushFrames) == 0 {
			if found := subnormaltest.Find(p, 5); len(found) > 0 {
				t.Fatalf("after the sweep at %.2f s the state holds subnormal numbers: %v", float64(frame)/rate, found)
			}
		}
	}
	subnormaltest.CheckPin(t, "piano held chord", hash, pinnedHeldChordHash)
}

// pinnedHeldChordHash is the float32 output of TestDecayingStateIsNeverSubnormal
// (16 s held, 30 s released, 48 kHz), produced alike on amd64 and arm64.
const pinnedHeldChordHash = 0
