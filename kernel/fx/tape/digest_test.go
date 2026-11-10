package tape

import (
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// tapeDigest pins the tape delay output bits for an exact test programme,
// through a smoothed control change. CI checks the same digest on arm64 in the
// PCM24 parity job.
const tapeDigest = "15fbc2d73da23244c345bfbe8c5ba6690e4c2eb80b40b850bf6cdc8a1c642951"

func TestTapeDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []Params{
		DefaultParams(),
		{Division: fx.FreeDelay, TimeMs: 180, Feedback: .85, Wow: 1, Flutter: .7, Saturation: .9, DampHz: 2_000},
		{Division: fx.QuarterDotted, Feedback: .2, Wow: .05, Flutter: 0, Saturation: 0, DampHz: 14_000},
	}
	for i, p := range settings {
		x, err := New([]int{48_000, 44_100, 96_000}[i], 97_500)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.SetParams(p); err != nil {
			t.Fatal(err)
		}
		left, right := bitdigest.NewNoise(uint32(i)+1), bitdigest.NewNoise(uint32(i)+2)
		for frame := 0; frame < 40_000; frame++ {
			if frame == 20_000 {
				if err := x.SetParams(settings[(i+1)%len(settings)]); err != nil {
					t.Fatal(err)
				}
			}
			d.Float32(x.Process(bitdigest.Program(left, right, frame)))
			// The float32 output rounds away most last-bit differences; the
			// float64 state does not.
			d.Float64(x.feedbackState.left, x.feedbackState.right, x.feedback, x.damp, x.wow, x.flutter, x.sat)
		}
	}
	if got := d.Hex(); got != tapeDigest {
		t.Fatalf("tape digest = %s, want %s", got, tapeDigest)
	}
}
