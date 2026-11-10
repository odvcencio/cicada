package ampcab

import (
	"runtime"
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// ampcabDigest pins the amp and cabinet output bits for an exact test
// programme, through each cabinet and a smoothed control change. Its
// multiply-adds are rounded, but it still calls math.Tanh per sample and
// math.Exp for its coefficients, whose last bits differ on arm64: the amp is
// linked into the core WebAssembly kernel, whose size budget cannot also hold
// kernel/dsp/detmath. Until it can, the digest is pinned on amd64 only.
const ampcabDigest = "fca9fae912642a187470b277bff4f37cdee144531f5992429159d2a9a2c2b471"

func TestAmpcabDigest(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amp/cab calls math.Tanh and math.Exp, whose last bits differ on " + runtime.GOARCH + "; see ampcabDigest")
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
