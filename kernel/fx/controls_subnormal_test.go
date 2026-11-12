package fx

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// Smoothed controls that glide to a target of zero, and followers that decay
// toward zero in silence, stall at subnormal numbers in float64 a few seconds
// later and slow every frame from then on. Each effect sweeps them every
// subnormal.SweepFrames of its own frames. Each case moves the controls at
// 1 s; with the build tag cicada_noflush it must reach the stall (Stalls),
// and its output must match the same pin in both builds.
func TestControlsAreNeverSubnormal(t *testing.T) {
	const rate = 48000
	swept := func(frames *uint32) func() bool {
		return func() bool { return *frames%(64*subnormal.SweepFrames) == 0 }
	}
	t.Run("delay", func(t *testing.T) {
		d, err := NewDelay(rate, 120_000)
		if err != nil {
			t.Fatal(err)
		}
		p := DefaultDelayParams()
		p.PingPong = true
		if err := d.SetParams(p); err != nil {
			t.Fatal(err)
		}
		d.Reset()
		p.Feedback, p.Width, p.Mix, p.PingPong = 0, 0, 0, false
		noise := subnormaltest.Noise(7)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "delay controls to zero", Rate: rate, Seconds: 8, State: d, Swept: swept(&d.frames),
			Next: func(frame int) (float32, float32) {
				if frame == rate {
					if err := d.SetParams(p); err != nil {
						t.Fatal(err)
					}
				}
				x := float32(noise() * .1)
				return d.Process(x, x)
			},
			Stalls: []string{".feedback", ".ping", ".width", ".mix"},
			Pinned: pinnedDelayControlsHash,
		})
	})
	t.Run("compressor", func(t *testing.T) {
		c, err := NewCompressor(rate)
		if err != nil {
			t.Fatal(err)
		}
		p := DefaultCompParams()
		p.ReleaseMs, p.MakeupAuto, p.MakeupDB = 10, false, 6
		if err := c.SetParams(p); err != nil {
			t.Fatal(err)
		}
		c.Reset()
		p.Threshold, p.Knee, p.MakeupDB, p.Mix = 0, 0, 0, 0
		noise := subnormaltest.Noise(11)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "compressor controls to zero", Rate: rate, Seconds: 10, State: c, Swept: swept(&c.frames),
			Next: func(frame int) (float32, float32) {
				if frame == rate {
					if err := c.SetParams(p); err != nil {
						t.Fatal(err)
					}
				}
				// The external sidechain is loud for a second, then silent:
				// the reduction releases to zero and the sidechain filter
				// decays to zero while the program keeps playing.
				x := float32(noise() * .1)
				var side float32
				if frame < rate {
					side = float32(x * 8)
				}
				return c.ProcessSidechain(x, x, side, side)
			},
			Stalls: []string{".params.Threshold", ".params.Knee", ".makeup", ".mix", ".reduction", ".sideLP.left", ".sideLP.right"},
			Pinned: pinnedCompressorControlsHash,
		})
	})
	t.Run("de-esser", func(t *testing.T) {
		p := DefaultDeesserParams()
		p.ReleaseMs = 10
		d, err := NewDeesser(rate, p)
		if err != nil {
			t.Fatal(err)
		}
		noise := subnormaltest.Noise(13)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "de-esser release in silence", Rate: rate, Seconds: 10, State: d, Swept: swept(&d.frames),
			Next: func(frame int) (float32, float32) {
				var x float32
				if frame < rate {
					x = float32(noise() * .5)
				}
				return d.Process(x, x)
			},
			Stalls: []string{".envelope", ".reduction"},
			Pinned: pinnedDeesserReleaseHash,
		})
		if subnormal.Enabled && !d.Silent() {
			t.Fatal("the de-esser is not Silent after 9 s of silence")
		}
	})
	t.Run("drive", func(t *testing.T) {
		d, err := NewDrive(rate)
		if err != nil {
			t.Fatal(err)
		}
		p := DefaultDriveParams()
		// A tone filter with a coefficient above 1/2 rounds its last step to
		// exactly zero; the darkest setting (1 kHz) stalls instead.
		p.GainDB, p.ToneHz = 12, 1_000
		if err := d.SetParams(p); err != nil {
			t.Fatal(err)
		}
		d.Reset()
		p.Shape, p.Mix = Hard, 0
		noise := subnormaltest.Noise(17)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "drive shape switch, mix to zero, then silence", Rate: rate, Seconds: 8, State: d, Swept: swept(&d.frames),
			Next: func(frame int) (float32, float32) {
				if frame == rate {
					if err := d.SetParams(p); err != nil {
						t.Fatal(err)
					}
				}
				var x float32
				if frame < 2*rate {
					x = float32(noise() * .1)
				}
				return d.Process(x, x)
			},
			Stalls: []string{".mix", ".weights[0]", ".toneState[0].left", ".toneState[1].left"},
			Pinned: pinnedDriveControlsHash,
		})
	})
}

// The pinned hashes are the float32 output of TestControlsAreNeverSubnormal,
// produced alike on amd64 and arm64, with and without the build tag
// cicada_noflush.
const (
	pinnedDelayControlsHash      = 0x3fc77a22089716d8
	pinnedCompressorControlsHash = 0xa117b6dcbb61aaa5
	pinnedDeesserReleaseHash     = 0x4aff7e4b1fbc68e9
	pinnedDriveControlsHash      = 0x5d385de79e32fb89
)
