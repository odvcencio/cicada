package fx

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// After a burst the reverb's tail decays in its diffusers and delay lines; it
// must reach zero rather than stall in float64 subnormal numbers, which made a
// silent reverb five times as expensive after 30 seconds.
func TestReverbTailIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	r, err := NewReverb(rate)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetParams(DefaultReverbParams()); err != nil {
		t.Fatal(err)
	}
	seed := uint32(7)
	for frame := range 60 * rate {
		var x float32
		if frame < rate/2 {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			x = float32(int32(seed)) / (1 << 31)
		}
		r.Process(x, x)
		if frame%(5*rate) == 0 && frame > 0 {
			if found := subnormaltest.Find(r, 5); len(found) > 0 {
				t.Fatalf("at %d s the reverb holds subnormal numbers: %v", frame/rate, found)
			}
		}
	}
}
