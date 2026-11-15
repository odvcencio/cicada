package chorus

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// Depth and spread glide to targets of zero, and the input lowpass decays
// toward zero in silence; in float64 each stalls at a subnormal number seconds
// later and slows every frame. (At 44.1 and 48 kHz the lowpass coefficient is
// above 1/2 and its last step rounds to exactly zero; at 96 kHz it stalls,
// so the case runs at 96 kHz.) The chorus sweeps them every
// subnormal.SweepFrames frames. With the build tag cicada_noflush the case
// must reach the stall, and the output must match the same pin.
func TestChorusIsNeverSubnormal(t *testing.T) {
	const rate = 96000
	p, err := New(rate, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	params := DefaultParams()
	params.Depth, params.Spread = 0, 0
	noise := subnormaltest.Noise(19)
	subnormaltest.Run(t, subnormaltest.Case{
		Name: "chorus depth and spread to zero, then silence", Rate: rate, Seconds: 12, State: p,
		Swept: func() bool { return p.frames%(64*subnormal.SweepFrames) == 0 },
		Next: func(frame int) (float32, float32) {
			if frame == rate {
				if err := p.SetParams(params); err != nil {
					t.Fatal(err)
				}
			}
			var x float32
			if frame < 2*rate {
				x = float32(noise() * .1)
			}
			return p.Process(x, x)
		},
		Stalls: []string{".current.Depth", ".current.Spread", ".filteredL", ".filteredR"},
		Pinned: pinnedChorusHash,
	})
}

// pinnedChorusHash is the float32 output of TestChorusIsNeverSubnormal,
// produced alike on amd64 and arm64, with and without cicada_noflush.
const pinnedChorusHash = 0xe8bb845f61fa1485
