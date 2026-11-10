package mix

import (
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// mixDigest pins the bits of track gains and pans over a grid of settings, of
// a three-track mix, and of the safety limiter on a loud exact test programme.
// CI checks the same digest on arm64 in the PCM24 parity job.
const mixDigest = "0e14e34b013ceffbc99682eee714b57f3d2f8b626fd54f059abd62b3627af0fe"

func TestMixDigest(t *testing.T) {
	d := bitdigest.New()
	for gain := -60.0; gain <= 12; gain += .37 {
		for pan := -1.0; pan <= 1; pan += .125 {
			track := NewTrack(gain, pan, false)
			table := NewTrackTable(float32(gain), float32(pan))
			d.Float32(track.Left, track.Right, table.Left, table.Right, DBToLinear(float32(gain)), DBToLinearClamped(float32(gain)))
		}
	}
	tracks := []Track{NewTrack(-3, -.4, false), NewTrack(-9.5, .7, false), NewTrackTable(-1.25, 0)}
	limiter, err := NewLimiter(48_000)
	if err != nil {
		t.Fatal(err)
	}
	noise := []*bitdigest.Noise{bitdigest.NewNoise(1), bitdigest.NewNoise(2), bitdigest.NewNoise(3), bitdigest.NewNoise(4)}
	for frame := 0; frame < 40_000; frame++ {
		var dry Dry
		for i, track := range tracks {
			l, r := bitdigest.Program(noise[i], noise[3], frame)
			dry.Add(l*4, r*4, track)
		}
		dry.AddReturn(noise[3].Next(6), noise[3].Next(6))
		left, right := dry.Music()
		l, r, ready := limiter.Process(left, right)
		if ready {
			d.Float32(l, r)
		}
		if frame%256 == 0 {
			d.Float64(limiter.GainReductionDB())
		}
	}
	if got := d.Hex(); got != mixDigest {
		t.Fatalf("mix digest = %s, want %s", got, mixDigest)
	}
}
