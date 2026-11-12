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
	// The compressor keeps compressing program audio at mix 1 while its
	// threshold, knee and makeup glide to 0 and its external sidechain goes
	// silent, so its gain reduction releases to zero. The reduction and the
	// makeup stall too but are not flushed (Unflushed): they feed
	// fastmath.Exp2, which is exactly 1 only for an exact 0, so flushing them
	// changed this output from about 8 s on. The pin holds in both builds.
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
		p.Threshold, p.Knee, p.MakeupDB = 0, 0, 0
		noise := subnormaltest.Noise(11)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "compressor controls to zero", Rate: rate, Seconds: 12, State: c, Swept: swept(&c.frames),
			Next: func(frame int) (float32, float32) {
				if frame == rate {
					if err := c.SetParams(p); err != nil {
						t.Fatal(err)
					}
				}
				// The external sidechain is loud for a second, then silent,
				// while the program keeps playing through the compressor.
				x := float32(noise() * .05)
				var side float32
				if frame < rate {
					side = float32(x * 16)
				}
				return c.ProcessSidechain(x, x, side, side)
			},
			Stalls:    []string{".params.Threshold", ".params.Knee", ".sideLP.left", ".sideLP.right", ".makeup", ".reduction"},
			Unflushed: []string{".makeup", ".reduction"},
			Pinned:    pinnedCompressorControlsHash,
		})
	})
	t.Run("compressor mix", func(t *testing.T) {
		c, err := NewCompressor(rate)
		if err != nil {
			t.Fatal(err)
		}
		p := DefaultCompParams()
		p.Threshold, p.MakeupAuto, p.MakeupDB = -30, false, 3
		if err := c.SetParams(p); err != nil {
			t.Fatal(err)
		}
		c.Reset()
		p.Mix = 0
		noise := subnormaltest.Noise(12)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "compressor mix to zero", Rate: rate, Seconds: 8, State: c, Swept: swept(&c.frames),
			Next: func(frame int) (float32, float32) {
				if frame == rate {
					if err := c.SetParams(p); err != nil {
						t.Fatal(err)
					}
				}
				x := float32(noise() * .2)
				return c.Process(x, x)
			},
			Stalls:    []string{".mix"},
			Unflushed: []string{".reduction"},
			Pinned:    pinnedCompressorMixHash,
		})
	})
	// The de-esser hears sibilant noise, then nine seconds of silence, in
	// which its detector envelope stalls, then quiet program again: the
	// output after the stall is what a flush could change. The reduction
	// stalls too but is not flushed (Unflushed): it feeds fastmath.Exp2.
	t.Run("de-esser", func(t *testing.T) {
		p := DefaultDeesserParams()
		p.ReleaseMs = 10
		d, err := NewDeesser(rate, p)
		if err != nil {
			t.Fatal(err)
		}
		noise := subnormaltest.Noise(13)
		subnormaltest.Run(t, subnormaltest.Case{
			Name: "de-esser release in silence", Rate: rate, Seconds: 12, State: d, Swept: swept(&d.frames),
			Next: func(frame int) (float32, float32) {
				var x float32
				switch {
				case frame < rate:
					x = float32(noise() * .5)
				case frame >= 10*rate:
					x = float32(noise() * .002)
				}
				if frame == 10*rate && !subnormal.Enabled {
					// The program resumes now; the envelope has stalled in
					// the silence before it.
					subnormaltest.CheckStalls(t, "de-esser envelope before the program resumes", d, []string{".envelope"})
				}
				return d.Process(x, x)
			},
			Stalls:    []string{".reduction"},
			Unflushed: []string{".reduction"},
			Pinned:    pinnedDeesserReleaseHash,
		})
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
	pinnedCompressorControlsHash = 0xf8b8e53197c5fd45
	pinnedCompressorMixHash      = 0x314866db2a549811
	pinnedDeesserReleaseHash     = 0xc51360ea22fe0271
	pinnedDriveControlsHash      = 0x5d385de79e32fb89
)
