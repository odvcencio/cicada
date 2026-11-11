package chorus

import (
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// chorusDigest pins the chorus output bits for an exact test programme, with
// one to three voices and a smoothed control change. CI checks the same digest
// on arm64 in the PCM24 parity job. It moved on 2026-11-10 (pull request
// #327) when the smoothing and low-pass coefficients (Exp) moved from the math
// library to kernel/dsp/detmath.
const chorusDigest = "e5f0bcd0f30a75fae5762fda5ac0ba01502a23f4405a6e2a6f3179577787cbce"

func TestChorusDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []Params{
		DefaultParams(),
		{Voices: 1, Rate: 2.5, Depth: 7, Spread: .3, Mix: .8},
		{Voices: 2, Rate: .15, Depth: 1.5, Spread: .9, Mix: .35},
	}
	for i, p := range settings {
		x, err := New([]int{48_000, 44_100, 96_000}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		left, right := bitdigest.NewNoise(uint32(i)+1), bitdigest.NewNoise(uint32(i)+2)
		for frame := 0; frame < 30_000; frame++ {
			if frame == 15_000 {
				// The voice count is fixed at construction; the other
				// controls move to the next setting.
				next := settings[(i+1)%len(settings)]
				next.Voices = p.Voices
				if err := x.SetParams(next); err != nil {
					t.Fatal(err)
				}
			}
			d.Float32(x.Process(bitdigest.Program(left, right, frame)))
			// The float32 output rounds away most last-bit differences; the
			// float64 state does not.
			d.Float64(x.current.Rate, x.current.Depth, x.current.Spread, x.current.Mix, x.filteredL, x.filteredR)
			d.Int(int(x.slowPhase[0]), int(x.slowPhase[1]), int(x.slowPhase[2]))
		}
	}
	if got := d.Hex(); got != chorusDigest {
		t.Fatalf("chorus digest = %s, want %s", got, chorusDigest)
	}
}
