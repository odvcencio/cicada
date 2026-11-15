//go:build !cicada_noflush

package subnormal

import "math"

// Enabled is true when Flush and Flush64 flush; the build tag cicada_noflush
// turns them into the identity, for the check that flushing leaves 24-bit
// output unchanged (make test-flush-identity).
const Enabled = true

// Flush returns x, or a zero with x's sign when x is subnormal.
func Flush(x float32) float32 {
	bits := math.Float32bits(x)
	if bits&exponentMask == 0 {
		return math.Float32frombits(bits & 0x80000000)
	}
	return x
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
