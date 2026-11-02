// Package fixture computes the digest that the native tests and the TinyGo
// build compare: 4,096 grid values of every source kind, hashed. It uses
// integer arithmetic only, so it builds for the browser module unchanged.
package fixture

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"

	"m31labs.dev/cicada/kernel/modulate"
)

// ExpectedDigest is the SHA-256 of the fixture's values, hex encoded.
const ExpectedDigest = "5e97e5e2c5d1d9a934c681417513a38c6b3417af2afd70387e514dd6d82344ea"

// Tick returns the tick and its remainder over 60000*rate for a song sample
// at a tempo in thousandths of a beat per minute, at 960 ticks per quarter.
func Tick(sample, bpmMilli, rate uint64) (tick, rem, den uint64) {
	den = 60_000 * rate
	hi, lo := bits.Mul64(sample, bpmMilli*960)
	tick, rem = bits.Div64(hi, lo, den)
	return tick, rem, den
}

// Digest hashes the fixture values.
func Digest() [32]byte {
	h := sha256.New()
	var buf [4]byte
	points := []modulate.Point{
		{At: 0, Value: -modulate.One, Shape: modulate.Linear},
		{At: 1 << 30, Value: modulate.One, Shape: modulate.Smooth},
		{At: 2 << 30, Value: 0, Shape: modulate.Exponential},
		{At: 3 << 30, Value: -modulate.One / 2, Shape: modulate.Step},
		{At: 1 << 32, Value: modulate.One / 4, Shape: modulate.Linear},
	}
	for g := uint64(0); g < 4096; g++ {
		tick, rem, den := Tick(g*modulate.GridFrames, 133_333, 44100)
		p := modulate.SyncedPosition(tick, rem, den, 7680)
		f := modulate.FreePosition(g*modulate.GridFrames, 44100, 70_000)
		for _, v := range []int32{
			modulate.LFO(modulate.Sine, 4242, 0, p), modulate.LFO(modulate.Triangle, 4242, 0, p),
			modulate.LFO(modulate.Square, 4242, 0, p), modulate.LFO(modulate.SampleHold, 4242, 2, f),
			modulate.Sway(4242, 3, 3, f), modulate.Contour(points, p),
		} {
			binary.LittleEndian.PutUint32(buf[:], uint32(v))
			h.Write(buf[:])
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
