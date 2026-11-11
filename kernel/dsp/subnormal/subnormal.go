// Package subnormal keeps decaying recursive state out of subnormal numbers.
//
// A recursive filter, resonator or envelope whose state decays by a factor
// close to 1 each sample reaches float32's subnormal range (magnitudes below
// 2^-126) and, with round-to-nearest, often stops decaying there: x*d rounds
// back to x once x is a few dozen subnormal steps. Most processors handle a
// subnormal operand or result many times slower than a normal one (an Intel
// core needs a microcode assist for each), so a ringing voice or an effect's
// tail gets slower the longer it rings, and stays slow in silence. WebAssembly
// has no flush-to-zero mode, and setting one on a native processor would make
// native and WebAssembly output differ, so this package flushes in code: Flush
// replaces a subnormal state value with a zero of the same sign, the same way
// on every target.
//
// What a flush can change. Let v be a flushed value (|v| < 2^-126) and G the
// largest gain from that state to an output sample through the processor's
// linear and bounded nonlinear stages. The difference it makes is at most
// |v|*G < G*2^-126 in any later value. Float32 addition absorbs a difference
// smaller than half a unit in the last place of the other operand, so every
// value of magnitude 2^-102*G or more comes out identical, and a difference
// can survive only in values smaller than that. Each processor that uses Flush
// states its G; for G <= 2^24, a changed output sample is below 2^-78:
//
//   - 16-bit and 24-bit PCM, as the WAV encoder writes them, are unchanged.
//     The encoder rounds x*(2^23-1) + d in float64, d being the dither. A
//     changed x is below 2^-78, so its scaled value is below 2^-55. When
//     |d| >= 1/4 that is under half a unit in the last place of d (at least
//     2^-55), so the sum is d either way; when |d| < 1/4, or there is no
//     dither, both sums lie within 1/4 + 2^-55 of zero and round to zero.
//   - float32 output (32-bit WAV, stems, the live engine's float buffers) can
//     differ only in samples smaller than 2^-78, about -470 dBFS.
//
// Flush adds a compare and a branch per state value; call it where the state
// is stored, not on every intermediate value.
package subnormal

import "math"

// exponentMask selects a float32's exponent bits; they are all zero for zero
// and for subnormal numbers.
const exponentMask = 0x7f800000

// Flush returns x, or a zero with x's sign when x is subnormal.
func Flush(x float32) float32 {
	bits := math.Float32bits(x)
	if bits&exponentMask == 0 {
		return math.Float32frombits(bits & 0x80000000)
	}
	return x
}

// Is reports whether x is subnormal: nonzero with a zero exponent.
func Is(x float32) bool {
	bits := math.Float32bits(x)
	return bits&exponentMask == 0 && bits&0x7fffffff != 0
}

// Flush64 returns x, or a zero with x's sign when x is a subnormal float64
// (magnitude below 2^-1022). A processor that keeps float64 state and writes
// float32 output loses nothing it could deliver: a difference below
// 2^-1022*G rounds to zero in float32 for any gain G below 2^872.
func Flush64(x float64) float64 {
	bits := math.Float64bits(x)
	if bits&0x7ff0000000000000 == 0 {
		return math.Float64frombits(bits & (1 << 63))
	}
	return x
}
