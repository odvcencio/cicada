package piano

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// A held chord, its release and the soundboard's ringing in the silence after
// must never leave state in subnormal numbers, which most processors handle
// many times slower than normal ones. Before the flush, the strings and the
// resonators stalled there and a held chord cost 30 times more after 16 s.
// The pedal case presses and releases the sustain pedal: the smoothed pedal
// value then decays toward zero and, unswept, stalled at 1.4e-43 and made the
// 88 sympathetic resonators 12 to 19 times as expensive.
// The output is pinned: no voice golden runs long enough to see a flush.
// Both pins also hold in a build with the tag cicada_noflush, where nothing
// is flushed: the piano's flushes change no output bit in these renders.
func TestDecayingStateIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	for _, c := range []struct {
		name                        string
		seconds                     int
		pedalDown, pedalUp, noteOff int // seconds; pedalDown 0 means no pedal
		pinned                      uint64
	}{
		{name: "piano held chord", seconds: 46, noteOff: 16, pinned: pinnedHeldChordHash},
		{name: "piano pedal press and release", seconds: 20, pedalDown: 1, pedalUp: 2, noteOff: 3, pinned: pinnedPedalHash},
	} {
		t.Run(c.name, func(t *testing.T) {
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
			for frame := range c.seconds * rate {
				switch {
				case c.pedalDown != 0 && frame == c.pedalDown*rate:
					if err := p.SetSustain(1); err != nil {
						t.Fatal(err)
					}
				case c.pedalDown != 0 && frame == c.pedalUp*rate:
					if err := p.SetSustain(0); err != nil {
						t.Fatal(err)
					}
				}
				if frame == c.noteOff*rate {
					for _, note := range chord {
						p.NoteOff(note)
					}
				}
				hash.Add(p.NextStereo())
				// The state is swept every flushFrames samples; between sweeps a
				// value may sit in subnormal numbers for those few samples.
				if subnormal.Enabled && p.frames%(64*flushFrames) == 0 {
					if found := subnormaltest.Find(p, 5); len(found) > 0 {
						t.Fatalf("after the sweep at %.2f s the state holds subnormal numbers: %v", float64(frame)/rate, found)
					}
				}
			}
			subnormaltest.CheckPin(t, c.name, hash, c.pinned)
		})
	}
}

// pinnedHeldChordHash is the float32 output of TestDecayingStateIsNeverSubnormal
// (16 s held, 30 s released, 48 kHz), produced alike on amd64 and arm64.
const pinnedHeldChordHash = 0xdec0db2755ca2d92

// pinnedPedalHash is the float32 output of the pedal case (pedal down at 1 s,
// up at 2 s, notes off at 3 s, 20 s, 48 kHz), produced alike on amd64 and
// arm64. It is the same with and without the pedal's flush: the pedal reaches
// the output only through sums with values near 1, which absorb it.
const pinnedPedalHash = 0xe64e06a0f60f7b75
