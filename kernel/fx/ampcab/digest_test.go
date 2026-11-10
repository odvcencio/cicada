package ampcab

import (
	"runtime"
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// ampcabDigest pins the amp and cabinet output bits and its float64 state for
// an exact test programme, through each cabinet and a smoothed control change.
// Its multiply-adds are rounded, but it still calls math.Tanh on every
// oversampled sample (ampcab.go, the four Tanh calls in process) and math.Exp,
// math.Cos and math.Pow for its coefficients and gains. Their last bits differ
// on arm64 and WebAssembly, which moves the float64 state there; the float32
// output has matched on all three targets (see TestAmpcabOutputDigest). So this
// digest is pinned on amd64 only, until the follow-up to pull request #316
// moves the amp to kernel/dsp/detmath under Decision 9 and removes the skip.
const ampcabDigest = "fca9fae912642a187470b277bff4f37cdee144531f5992429159d2a9a2c2b471"

func TestAmpcabDigest(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amp/cab calls math.Tanh, Exp, Cos and Pow, whose last bits move its float64 state on " + runtime.GOARCH + "; see ampcabDigest")
	}
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

// ampcabOutputDigest pins only the float32 output of the same programme. It
// runs on every target: the output matches on amd64, arm64 and WebAssembly
// even though the float64 state does not (see ampcabDigest).
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
