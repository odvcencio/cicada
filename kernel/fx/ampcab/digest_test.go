package ampcab

import (
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// ampcabDigest pins the amp and cabinet output bits and its float64 state for
// an exact test programme, through each cabinet and a smoothed control change.
// Its multiply-adds are rounded, and its Tanh, Exp, Cos and Pow come from
// kernel/dsp/detmath (the per-sample saturation on the second tier, TanhNear),
// so the digest is the same on amd64, arm64 and WebAssembly and runs on every
// target. (Until 2026-11-10 it called the math library, whose
// last bits differ on arm64 and WebAssembly, and was pinned on amd64 only.)
const ampcabDigest = "7b2999b557c5848120a298294676cea240f43400528b13af602051c8f637bdec"

func TestAmpcabDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []Params{
		DefaultParams(),
		{Drive: .9, Tone: .2, Output: -6, Cabinet: 1},
		{Drive: .1, Tone: .95, Output: 4.5, Cabinet: 2},
	}
	for i, p := range settings {
		x, err := New([]int{48_000, 44_100, 96_000}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		left, right := bitdigest.NewNoise(uint32(i)+1), bitdigest.NewNoise(uint32(i)+2)
		for frame := 0; frame < 30_000; frame++ {
			if frame == 15_000 {
				if err := x.SetParams(settings[(i+1)%len(settings)]); err != nil {
					t.Fatal(err)
				}
			}
			d.Float32(x.Process(bitdigest.Program(left, right, frame)))
			// The float32 output rounds away most last-bit differences; the
			// float64 state does not.
			d.Float64(x.drive, x.tone, x.out, x.bypass, x.weights[0], x.weights[1], x.weights[2], x.left.tone, x.right.tone, x.left.dc, x.right.dc)
		}
	}
	if got := d.Hex(); got != ampcabDigest {
		t.Fatalf("amp/cab digest = %s, want %s", got, ampcabDigest)
	}
}

// ampcabOutputDigest pins only the float32 output of the same programme, on
// every target. It predates ampcabDigest running everywhere and is kept as the
// pin a change to the amp's sound must move.
const ampcabOutputDigest = "96dde02aa1b2c820208df1c10ca9bf8c01d80d7b80dfeee64a887371b70c2d38"

func TestAmpcabOutputDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []Params{
		DefaultParams(),
		{Drive: .9, Tone: .2, Output: -6, Cabinet: 1},
		{Drive: .1, Tone: .95, Output: 4.5, Cabinet: 2},
	}
	for i, p := range settings {
		x, err := New([]int{48_000, 44_100, 96_000}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		left, right := bitdigest.NewNoise(uint32(i)+1), bitdigest.NewNoise(uint32(i)+2)
		for frame := 0; frame < 30_000; frame++ {
			if frame == 15_000 {
				if err := x.SetParams(settings[(i+1)%len(settings)]); err != nil {
					t.Fatal(err)
				}
			}
			d.Float32(x.Process(bitdigest.Program(left, right, frame)))
		}
	}
	if got := d.Hex(); got != ampcabOutputDigest {
		t.Fatalf("amp/cab output digest = %s, want %s", got, ampcabOutputDigest)
	}
}
