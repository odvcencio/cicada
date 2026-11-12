package acid

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// Resonance, envelope modulation and accent glide to live targets, and the
// savage blend glides to zero when the switch goes off; in float64 each
// stalls at a subnormal number seconds after a target of zero (the savage
// blend, with its 20 ms glide, after about 15 s) and slows every sample.
// The voice sweeps them every subnormal.SweepFrames samples. With the build
// tag cicada_noflush the case must reach the stall.
func TestControlsAreNeverSubnormal(t *testing.T) {
	const rate = 48000
	v, err := New(rate)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	p.Savage = true
	if err := v.SetParams(p); err != nil {
		t.Fatal(err)
	}
	// The switch changes with SetParams; the numbers glide with
	// SetParamsTarget.
	p.Savage = false
	target := p
	target.Resonance, target.EnvMod, target.Accent = 0, 0, 0
	v.NoteOn(45, true, false, 100)
	subnormaltest.Run(t, subnormaltest.Case{
		Name: "acid controls to zero", Rate: rate, Seconds: 18, State: v,
		Swept: func() bool { return v.frames%(64*subnormal.SweepFrames) == 0 },
		Next: func(frame int) (float32, float32) {
			if frame == rate {
				if err := v.SetParams(p); err != nil {
					t.Fatal(err)
				}
				if err := v.SetParamsTarget(target, .005); err != nil {
					t.Fatal(err)
				}
			}
			x := v.Next()
			return x, x
		},
		Stalls: []string{".params.Resonance", ".params.EnvMod", ".params.Accent", ".savageBlend"},
		Pinned: pinnedAcidControlsHash,
	})
}

// pinnedAcidControlsHash is zero: the output is not pinned, because the acid
// voice's float32 output differs between architectures without any flush.
// This case gave 0x2bae0163d409ab79 on amd64 and 0x65564691e809b4e1 on arm64
// (macOS), each the same with and without cicada_noflush, so the flush
// changes no output bit on either. The voice computes its coefficients with
// the math library (math.Exp in SetParams and NoteOn), the likely cause.
const pinnedAcidControlsHash = 0
