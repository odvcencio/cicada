package acid

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// Resonance glides to a live target and the savage blend to zero when the
// switch goes off; in float64 each stalls at a subnormal number seconds after
// a target of zero (the savage blend, with its 20 ms glide, after about 15 s)
// and slows every sample. The voice sweeps them every subnormal.SweepFrames
// samples. Envelope modulation and accent glide to zero and stall as well,
// but are not flushed (Unflushed): they feed the cutoff through
// fastmath.Exp2, which is exactly 1 only for an exact 0, so flushing them
// changed this output. Notes keep playing after every stall, so a flush that
// changes the sound changes the pin. With the build tag cicada_noflush the
// case must reach the stall and give the same pin.
//
// The pin covers seconds 2 to 18 only (PinFromSecond). The acid voice's
// first second differs between amd64 and arm64 on main, without any flush:
// it computes its coefficients with the math library (math.Exp in SetParams
// and NoteOn). The whole-output hash is left unpinned until the determinism
// stack (#328, #329, #343) moves those calls to kernel/dsp/detmath.
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
			// A note every quarter second, every fourth one accented.
			switch frame % (rate / 4) {
			case 0:
				step := frame / (rate / 4)
				v.NoteOn(uint8(45+step%5), step%4 == 0, false, 100)
			case rate / 10:
				v.NoteOff()
			}
			x := v.Next()
			return x, x
		},
		Stalls:        []string{".params.Resonance", ".savageBlend", ".params.EnvMod", ".params.Accent"},
		Unflushed:     []string{".params.EnvMod", ".params.Accent"},
		PinFromSecond: 2,
		Pinned:        pinnedAcidControlsFromSecond2Hash,
	})
}

// pinnedAcidControlsFromSecond2Hash is the hash of seconds 2 to 18 of
// TestControlsAreNeverSubnormal's output (Hash.From), produced alike on amd64
// and arm64, with and without cicada_noflush.
const pinnedAcidControlsFromSecond2Hash = 0x2f34655096e846ec
