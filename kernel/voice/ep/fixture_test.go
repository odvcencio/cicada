package ep_test

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/voice/ep/testdata/fixture"
)

func TestEpNativeFixtureGolden(t *testing.T) {
	pcm := make([]float32, fixture.Frames*2)
	for index, expected := range fixture.GoldenHashes {
		if err := fixture.Render(index, 128, pcm); err != nil {
			t.Fatal(err)
		}
		hash := uint64(14695981039346656037)
		for _, value := range pcm {
			bits := math.Float32bits(value)
			for range 4 {
				hash ^= uint64(byte(bits))
				hash *= 1099511628211
				bits >>= 8
			}
		}
		if hash != expected {
			t.Errorf("fixture %d changed: %016x, expected %016x", index, hash, expected)
		}
	}
}
