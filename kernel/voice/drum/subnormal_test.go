package drum

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// A lane set to -1000 dB glides its level to exactly zero, and a lane panned
// hard left its right gain (sin 0); in float64 each stalls at a subnormal
// number seconds later and slows every frame. The kit sweeps them every
// subnormal.SweepFrames frames. With the build tag cicada_noflush the case
// must reach the stall.
//
// The flush changes the float32 output, in the sign of zero only: unflushed,
// a hit's negative half times the stalled level is a negative subnormal sum
// that converts to -0; flushed, the sum is +0 + -0 = +0. From 3.95 s on,
// 97,343 of the 768,000 output samples are +0 instead of -0 (compared bit by
// bit on amd64); 16-bit and 24-bit PCM encode the two alike.
func TestControlsAreNeverSubnormal(t *testing.T) {
	const rate = 48000
	k, err := New(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultParams(BD)
	p.LevelDB, p.Pan = -1000, -1
	subnormaltest.Run(t, subnormaltest.Case{
		Name: "drum level and pan to zero", Rate: rate, Seconds: 8, State: k,
		Swept: func() bool { return k.frames%(64*subnormal.SweepFrames) == 0 },
		Next: func(frame int) (float32, float32) {
			if frame == rate {
				if err := k.SetParamsTarget(BD, p, .005); err != nil {
					t.Fatal(err)
				}
			}
			if frame%(rate/2) == 0 {
				k.Hit(BD, 110, false)
			}
			return k.NextStereo()
		},
		Stalls:             []string{".lanes[0].level", ".lanes[0].panR"},
		Pinned:             pinnedDrumControlsHash,
		PinnedWithoutFlush: pinnedDrumControlsWithoutFlushHash,
	})
}

// The pinned hashes are the float32 output of TestControlsAreNeverSubnormal,
// produced alike on amd64 and arm64, with the flush and with cicada_noflush.
const (
	pinnedDrumControlsHash             = 0x00a60c6d3ee57f76
	pinnedDrumControlsWithoutFlushHash = 0xc446b213dc475ff6
)
