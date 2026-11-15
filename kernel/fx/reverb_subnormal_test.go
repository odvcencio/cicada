package fx

import (
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
)

// After a burst the reverb's tail decays in its diffusers, delay lines and
// damping filters; it must reach zero rather than stall in float64 subnormal
// numbers, which made a silent reverb five times as expensive after 30
// seconds. With the default 2.4 s decay the diffusers get there within the
// minute; the delay lines and damping filters need the shortest decay, 0.3 s.
//
// Two more cases stall while the reverb is fed. A send from a hard-left track
// feeds the right channel an exact zero, so the right input diffusers decay on
// their own while the left plays (16 ms instead of 4 ms of CPU per audio
// second before they were swept per channel). Automation that takes the mix
// to zero leaves the smoothed mix stalled at a subnormal number. Neither
// flush changes an output bit: the test pins the same output hash in a build
// with the tag cicada_noflush, where Flush64 returns its argument.
func TestReverbTailIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	short := DefaultReverbParams()
	short.DecaySec = .3
	dry := DefaultReverbParams()
	dry.Mix = 0
	for _, c := range []struct {
		name    string
		params  ReverbParams
		seconds int
		// input returns the frame's input; noise is the next noise sample.
		input func(frame int, noise func() float32) (float32, float32)
		// set, when not nil, takes effect at 1 s.
		set *ReverbParams
		// sameWithoutFlush is true when the flush changes no output bit, so
		// the pin holds with cicada_noflush too.
		sameWithoutFlush bool
		// stalls lists what holds subnormal numbers at the end without the
		// flush (subnormaltest.CheckStalls), the proof that the case reaches
		// the stall.
		stalls []string
		pinned uint64
	}{
		{name: "reverb tail", params: DefaultReverbParams(), seconds: 60, input: burst, pinned: pinnedReverbTailHash},
		{name: "reverb short tail", params: short, seconds: 60, input: burst, pinned: pinnedReverbShortTailHash},
		{name: "reverb hard-left send", params: DefaultReverbParams(), seconds: 30, sameWithoutFlush: true, stalls: []string{".input[1]"}, pinned: pinnedReverbHardLeftHash,
			input: func(frame int, noise func() float32) (float32, float32) {
				x := float32(noise() * .1)
				if frame < rate/2 {
					return x, x
				}
				return x, 0
			}},
		{name: "reverb mix to zero", params: DefaultReverbParams(), seconds: 10, set: &dry, sameWithoutFlush: true, stalls: []string{".mix"}, pinned: pinnedReverbMixToZeroHash,
			input: func(_ int, noise func() float32) (float32, float32) {
				x := float32(noise() * .1)
				return x, x
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := NewReverb(rate)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.SetParams(c.params); err != nil {
				t.Fatal(err)
			}
			seed := uint32(7)
			noise := func() float32 {
				seed ^= seed << 13
				seed ^= seed >> 17
				seed ^= seed << 5
				return float32(int32(seed)) / (1 << 31)
			}
			hash := subnormaltest.NewHash(rate)
			for frame := range c.seconds * rate {
				if c.set != nil && frame == rate {
					if err := r.SetParams(*c.set); err != nil {
						t.Fatal(err)
					}
				}
				hash.Add(r.Process(c.input(frame, noise)))
				if !subnormal.Enabled {
					continue
				}
				// Each part is swept every reverbFlushFrames frames of its
				// silent input, the mix every turn of the predelay buffer;
				// between sweeps a value may sit in subnormal numbers.
				swept := r.quiet != 0 && r.quiet%(64*reverbFlushFrames) == 0 ||
					r.quiet == 0 && r.quietIn[1] != 0 && r.quietIn[1]%(64*reverbFlushFrames) == 0 ||
					r.quietIn == [2]uint32{} && r.preIndex == 0
				if swept {
					if found := subnormaltest.Find(r, 5); len(found) > 0 {
						t.Fatalf("after the sweep at %.2f s the reverb holds subnormal numbers: %v", float64(frame)/rate, found)
					}
				}
			}
			if !subnormal.Enabled {
				subnormaltest.CheckStalls(t, c.name, r, c.stalls)
			}
			if !subnormal.Enabled && !c.sameWithoutFlush {
				t.Logf("built with cicada_noflush: the output differs from the pin by design; %s", hash.Report()[:40])
				return
			}
			subnormaltest.CheckPin(t, c.name, hash, c.pinned)
		})
	}
}

// burst is half a second of noise on both channels, then silence.
func burst(frame int, noise func() float32) (float32, float32) {
	if frame < 48000/2 {
		x := noise()
		return x, x
	}
	return 0, 0
}

// The pinned hashes are the float32 output of TestReverbTailIsNeverSubnormal
// at 48 kHz, produced alike on amd64 and arm64. The two tail hashes changed
// with the merge of #327, which computes the reverb's coefficients with
// kernel/dsp/detmath and its modulation with SinPi: they were
// 0xbf36da93f46a780b and 0xc1d7f1cf2d987f6c on the reverb before it.
// The tail and hard-left hashes changed again with the merge of the
// determinism stack (#328, #329, #343), which computes the modulation with
// SinPiNear: they were 0x73a3144619160bd7 and 0xe4a9e262c2bc2e0f. The short
// tail and mix-to-zero cases kept their bits.
const (
	pinnedReverbTailHash      = 0x87c4b246638b67fb
	pinnedReverbShortTailHash = 0xb6ac9e89d61b9eb3
	pinnedReverbHardLeftHash  = 0x261fd3b7ab4ef47e
	pinnedReverbMixToZeroHash = 0xf8cda5e8b8e3d6f9
)
