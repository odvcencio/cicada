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
const ExpectedDigest = "583a1a0d22afd47c5b88484572f1b9617029d853ca94a243ecf66fb248d04c6a"

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
	put := func(v int32) {
		binary.LittleEndian.PutUint32(buf[:], uint32(v))
		h.Write(buf[:])
	}
	// A 7.5 Hz sample-hold, the fastest rate a route accepts below 20 Hz.
	for g := uint64(0); g < 2048; g++ {
		put(modulate.LFO(modulate.SampleHold, 4242, 5, modulate.FreePosition(g*modulate.GridFrames, 48000, 7_500_000)))
	}
	// Positions near 2^40 samples (about 7 hours at 44.1 kHz) run the 128-bit
	// paths of both position functions.
	const far = uint64(1)<<40 - 4096*modulate.GridFrames
	for g := uint64(0); g < 2048; g++ {
		sample := far + g*modulate.GridFrames
		tick, rem, den := Tick(sample, 133_333, 44100)
		p := modulate.SyncedPosition(tick, rem, den, 1<<27+12345)
		f := modulate.FreePosition(sample, 44100, 19_999_999)
		put(modulate.LFO(modulate.Sine, 4242, 0, p.Offset(0xc0000000)))
		put(modulate.LFO(modulate.Triangle, 4242, 1, f))
		put(modulate.Sway(4242, 2, 4, f))
	}
	// Loop fitting: whole cycles in a loop, at and around the wrap.
	for _, loop := range []uint64{1, 1000, 8_000_000, 1<<35 + 17} {
		for _, cycles := range []uint64{1, 7, 4096} {
			for k := uint64(0); k < 64; k++ {
				at := loop * k / 64
				p := modulate.FitPosition(at, k%3, 3, loop, cycles)
				put(modulate.LFO(modulate.Sine, 4242, 3, p))
				put(int32(p.Phase))
				put(int32(p.Cycle))
			}
		}
	}
	// Offset carries into the cycle count.
	for _, phase := range []uint32{0, 1, 1 << 31, 0xffffffff} {
		p := modulate.Position{Cycle: 41, Phase: 0xffff0000}.Offset(phase)
		put(int32(p.Phase))
		put(int32(p.Cycle))
	}
	// An envelope follower driven through a rise, a hold and a long release.
	var f modulate.Follower
	for i := 0; i < 4096; i++ {
		peak := int32(0)
		switch {
		case i < 64:
			peak = modulate.One / 2
		case i < 128:
			peak = modulate.One
		case i%97 == 0:
			peak = modulate.One / 3
		}
		put(f.Step(peak, 1<<29, 1<<22))
	}
	f.Reset()
	put(f.Level)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
