package sample

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// A gain target of zero makes a looping note's gain glide toward zero; in
// float64 it stalls at a subnormal number seconds later and slows every frame
// of the note. The voice sweeps it every subnormal.SweepFrames smoothing steps. With the build
// tag cicada_noflush the case must reach the stall, and the output must match
// the same pin.
func TestGainIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	v := testVoice(t, testRegion(4800, true, true))
	trigger(t, v, 64, 100)
	subnormaltest.Run(t, subnormaltest.Case{
		Name: "sample gain to zero", Rate: rate, Seconds: 8, State: v,
		Swept: func() bool { return v.gainSteps != 0 && v.gainSteps%(64*subnormal.SweepFrames) == 0 },
		Next: func(frame int) (float32, float32) {
			if frame == rate {
				if err := v.SetGainTarget(0, .005); err != nil {
					t.Fatal(err)
				}
			}
			return v.NextStereo()
		},
		Stalls: []string{".gain"},
		Pinned: pinnedSampleGainHash,
	})
}

// pinnedSampleGainHash is the float32 output of TestGainIsNeverSubnormal,
// produced alike on amd64 and arm64, with and without cicada_noflush.
const pinnedSampleGainHash = 0x6233fc71680cd4b7
