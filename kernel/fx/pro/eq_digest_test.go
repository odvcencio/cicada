package pro

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// eqDigest is the SHA-256 of the coefficients and output of one band of every
// EQ type at every supported rate. amd64 never fuses a multiply and an add, but
// arm64 may unless an explicit conversion rounds the product first. For these
// bands the digest is the same on both; other frequencies or gains can still
// differ in the last bit through math.Sincos and math.Pow (see designBand). CI
// checks it on arm64 in the PCM24 parity job.
const eqDigest = "84a262a6f115646b9caa509c57de3b8c909b4712c73871cfdc21aaee0e1d56a5"

// digestNoise returns exact float32 noise in [-0.5, 0.5) from a 32-bit
// xorshift generator: a 24-bit integer divided by a power of two, so every
// target produces the same input.
func digestNoise(state *uint32) float32 {
	x := *state
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*state = x
	return float32(int32(x)>>8) / (1 << 24)
}

func TestEQDigestIsIdenticalOnEveryTarget(t *testing.T) {
	sum := sha256.New()
	var word [8]byte
	bands := []Band{
		{Enabled: true, Type: Peak, FrequencyHz: 1000, Q: 1.3, GainDB: 6},
		{Enabled: true, Type: LowShelf, FrequencyHz: 180, Q: 1, GainDB: -4.5},
		{Enabled: true, Type: HighShelf, FrequencyHz: 6000, Q: 1, GainDB: 3},
		{Enabled: true, Type: Highpass, FrequencyHz: 25, Q: .707},
		{Enabled: true, Type: Lowpass, FrequencyHz: 12000, Q: .9},
	}
	for _, rate := range []int{44100, 48000, 96000} {
		for _, band := range bands {
			eq, err := NewEQ(rate, [4]Band{band})
			if err != nil {
				t.Fatal(err)
			}
			f := eq.filters[0]
			for _, coefficient := range [...]float64{f.b0, f.b1, f.b2, f.a1, f.a2} {
				binary.LittleEndian.PutUint64(word[:], math.Float64bits(coefficient))
				sum.Write(word[:])
			}
			state := uint32(rate) + uint32(band.Type)<<20 + 1
			for i := 0; i < 24000; i++ {
				left, right := eq.Process(digestNoise(&state), digestNoise(&state))
				binary.LittleEndian.PutUint32(word[:4], math.Float32bits(left))
				binary.LittleEndian.PutUint32(word[4:], math.Float32bits(right))
				sum.Write(word[:])
			}
		}
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != eqDigest {
		t.Fatalf("EQ digest = %s, want %s", got, eqDigest)
	}
}
