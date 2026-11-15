package ampcab

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// A cabinet switch takes the old cabinet's weight to zero, the drive and the
// bypass blend can glide to zero, and the DC blocker and tone filter decay
// toward zero in silence; in float64 each stalls at a subnormal number seconds
// later and slows every frame (a stalled weight multiplies every
// impulse-response tap). The processor sweeps them every
// subnormal.SweepFrames frames. With the build tag cicada_noflush the case
// must reach the stall, and the output must match the same pin.
func TestAmpCabinetIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	params := DefaultParams()
	params.Bypass = true
	p, err := New(rate, params)
	if err != nil {
		t.Fatal(err)
	}
	params.Bypass, params.Drive, params.Cabinet = false, 0, 1
	noise := subnormaltest.Noise(29)
	subnormaltest.Run(t, subnormaltest.Case{
		Name: "amp and cabinet switch, then silence", Rate: rate, Seconds: 20, State: p,
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
		Stalls: []string{".drive", ".bypass", ".weights[0]", ".left.dc", ".left.tone"},
		Pinned: pinnedAmpCabinetHash,
	})
}

// pinnedAmpCabinetHash is the float32 output of TestAmpCabinetIsNeverSubnormal,
// produced alike on amd64 and arm64, with and without cicada_noflush.
const pinnedAmpCabinetHash = 0x940a08dc2ddf87a1
