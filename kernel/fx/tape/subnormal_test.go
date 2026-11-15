package tape

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// Feedback, wow, flutter and saturation glide to targets of zero; in float64
// each stalls at a subnormal number seconds later and slows every frame, and
// a stalled wow or flutter keeps the modulation sums running. The tape sweeps
// them every subnormal.SweepFrames frames. With the build tag cicada_noflush
// the case must reach the stall, and the output must match the same pin.
func TestTapeControlsAreNeverSubnormal(t *testing.T) {
	const rate = 48000
	tp, err := New(rate, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	params := DefaultParams()
	params.Feedback, params.Wow, params.Flutter, params.Saturation = 0, 0, 0, 0
	noise := subnormaltest.Noise(23)
	subnormaltest.Run(t, subnormaltest.Case{
		Name: "tape controls to zero", Rate: rate, Seconds: 8, State: tp,
		Swept: func() bool { return tp.frames%(64*subnormal.SweepFrames) == 0 },
		Next: func(frame int) (float32, float32) {
			if frame == rate {
				if err := tp.SetParams(params); err != nil {
					t.Fatal(err)
				}
			}
			x := float32(noise() * .1)
			return tp.Process(x, x)
		},
		Stalls: []string{".feedback", ".wow", ".flutter", ".sat"},
		Pinned: pinnedTapeControlsHash,
	})
}

// pinnedTapeControlsHash is the float32 output of
// TestTapeControlsAreNeverSubnormal, produced alike on amd64 and arm64, with
// and without cicada_noflush.
const pinnedTapeControlsHash = 0x1642a5ad8c5d86f1
