package fx

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// After a burst the reverb's tail decays in its diffusers, delay lines and
// damping filters; it must reach zero rather than stall in float64 subnormal
// numbers, which made a silent reverb five times as expensive after 30
// seconds. With the default 2.4 s decay the diffusers get there within the
// minute; the delay lines and damping filters need the shortest decay, 0.3 s.
func TestReverbTailIsNeverSubnormal(t *testing.T) {
	short := DefaultReverbParams()
	short.DecaySec = .3
	for _, c := range []struct {
		name   string
		params ReverbParams
		pinned uint64
	}{
		{"reverb tail", DefaultReverbParams(), pinnedReverbTailHash},
		{"reverb short tail", short, pinnedReverbShortTailHash},
	} {
		t.Run(c.name, func(t *testing.T) {
			const rate = 48000
			r, err := NewReverb(rate)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.SetParams(c.params); err != nil {
				t.Fatal(err)
			}
			seed := uint32(7)
			hash := subnormaltest.NewHash(rate)
			for frame := range 60 * rate {
				var x float32
				if frame < rate/2 {
					seed ^= seed << 13
					seed ^= seed >> 17
					seed ^= seed << 5
					x = float32(int32(seed)) / (1 << 31)
				}
				hash.Add(r.Process(x, x))
				// The damping filters are swept every reverbFlushFrames
				// frames; between sweeps a value may sit in subnormal
				// numbers for those few frames.
				if r.frames%(64*reverbFlushFrames) == 0 {
					if found := subnormaltest.Find(r, 5); len(found) > 0 {
						t.Fatalf("after the sweep at %.2f s the reverb holds subnormal numbers: %v", float64(frame)/rate, found)
					}
				}
			}
			subnormaltest.CheckPin(t, c.name, hash, c.pinned)
		})
	}
}

// The pinned hashes are the float32 output of TestReverbTailIsNeverSubnormal
// (0.5 s of noise, then silence, 60 s, 48 kHz), produced alike on amd64 and
// arm64.
const (
	pinnedReverbTailHash      = 0
	pinnedReverbShortTailHash = 0
)
