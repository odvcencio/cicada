package convolution

import (
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// convolutionDigest pins the convolution output bits for an exact test
// programme: a long stereo response through the head and the FFT tail, and a
// short mono response through the head alone. CI checks the same digest on
// arm64 in the PCM24 parity job.
const convolutionDigest = "c8ca3ec49055f9036e509b870f089f9d450bfb5302ff9244b910edfc9ae8441d"

// decayingImpulse returns exact noise whose level falls by 6 dB every 1,000
// frames.
func decayingImpulse(seed uint32, frames int) []float32 {
	n := bitdigest.NewNoise(seed)
	out := make([]float32, frames)
	for i := range out {
		out[i] = n.Next(min(i/1_000, 23))
	}
	return out
}

func TestConvolutionDigest(t *testing.T) {
	d := bitdigest.New()
	for i, c := range []struct {
		frames, partition int
		stereo            bool
	}{
		{12_000, 256, true},
		{3_000, 1024, false},
	} {
		left := decayingImpulse(uint32(i)+1, c.frames)
		var right []float32
		if c.stereo {
			right = decayingImpulse(uint32(i)+2, c.frames)
		}
		x, err := New(48_000, left, right, c.partition)
		if err != nil {
			t.Fatal(err)
		}
		in, other := bitdigest.NewNoise(uint32(i)+3), bitdigest.NewNoise(uint32(i)+4)
		for frame := 0; frame < 40_000; frame++ {
			d.Float32(x.Process(bitdigest.Program(in, other, frame)))
		}
	}
	if got := d.Hex(); got != convolutionDigest {
		t.Fatalf("convolution digest = %s, want %s", got, convolutionDigest)
	}
}
