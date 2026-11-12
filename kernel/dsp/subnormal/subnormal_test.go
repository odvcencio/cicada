package subnormal

import (
	"math"
	"testing"
)

func TestFlush(t *testing.T) {
	skipWithoutFlush(t)
	smallestNormal := math.Float32frombits(0x00800000)
	largestSubnormal := math.Float32frombits(0x007fffff)
	negativeZero := math.Float32frombits(0x80000000)
	for _, test := range []struct {
		in, want float32
	}{
		{0, 0},
		{negativeZero, negativeZero},
		{math.SmallestNonzeroFloat32, 0},
		{-math.SmallestNonzeroFloat32, negativeZero},
		{largestSubnormal, 0},
		{-largestSubnormal, negativeZero},
		{smallestNormal, smallestNormal},
		{-smallestNormal, -smallestNormal},
		{1, 1},
		{float32(math.Inf(-1)), float32(math.Inf(-1))},
	} {
		if got := Flush(test.in); math.Float32bits(got) != math.Float32bits(test.want) {
			t.Fatalf("Flush(%g [%08x]) = %08x, want %08x", test.in, math.Float32bits(test.in), math.Float32bits(got), math.Float32bits(test.want))
		}
		if Is(test.in) != (math.Float32bits(test.in) != math.Float32bits(test.want)) {
			t.Fatalf("Is(%08x) = %v", math.Float32bits(test.in), Is(test.in))
		}
	}
	nan := float32(math.NaN())
	if got := Flush(nan); got == got {
		t.Fatal("Flush changed a NaN")
	}
}

// A decaying value really does stop in the subnormal range without a flush:
// this is the behavior the package exists to prevent.
func TestDecayStallsInSubnormalsWithoutFlush(t *testing.T) {
	skipWithoutFlush(t)
	const decay = float32(.99765) // an 18 Hz DC blocker's pole at 48 kHz
	x := float32(1e-30)
	for range 1_000_000 {
		x = float32(x * decay)
	}
	if !Is(x) {
		t.Fatalf("after a million steps the state is %g, want a stalled subnormal", x)
	}
	y := float32(1e-30)
	for range 1_000_000 {
		y = Flush(float32(y * decay))
	}
	if y != 0 {
		t.Fatalf("with Flush the state is %g, want zero", y)
	}
}

func TestFlush64(t *testing.T) {
	skipWithoutFlush(t)
	negativeZero := math.Copysign(0, -1)
	for _, test := range []struct{ in, want float64 }{
		{0, 0},
		{negativeZero, negativeZero},
		{math.SmallestNonzeroFloat64, 0},
		{-math.SmallestNonzeroFloat64, negativeZero},
		{math.Float64frombits(0x000fffffffffffff), 0},
		{math.Float64frombits(0x0010000000000000), math.Float64frombits(0x0010000000000000)},
		{1e-300, 1e-300},
		{-2, -2},
	} {
		if got := Flush64(test.in); math.Float64bits(got) != math.Float64bits(test.want) {
			t.Fatalf("Flush64(%g) = %016x, want %016x", test.in, math.Float64bits(got), math.Float64bits(test.want))
		}
	}
}

func BenchmarkFlush(b *testing.B) {
	x := float32(.5)
	for range b.N {
		x = Flush(float32(x * .999))
		if x == 0 {
			x = .5
		}
	}
}

// skipWithoutFlush skips a test of the flushing itself in a build with the tag
// cicada_noflush, where Flush and Flush64 return their argument.
func skipWithoutFlush(t *testing.T) {
	t.Helper()
	if !Enabled {
		t.Skip("built with cicada_noflush: Flush and Flush64 return their argument")
	}
}

// With cicada_noflush, Flush and Flush64 must leave every value as it is.
func TestFlushIsIdentityWithoutFlush(t *testing.T) {
	if Enabled {
		t.Skip("only with the build tag cicada_noflush")
	}
	for _, bits := range []uint32{0, 0x80000000, 1, 0x807fffff, 0x00800000, 0x3f800000} {
		if got := math.Float32bits(Flush(math.Float32frombits(bits))); got != bits {
			t.Fatalf("Flush(%08x) = %08x", bits, got)
		}
	}
	for _, bits := range []uint64{0, 1 << 63, 1, 0x800fffffffffffff, 0x0010000000000000} {
		if got := math.Float64bits(Flush64(math.Float64frombits(bits))); got != bits {
			t.Fatalf("Flush64(%016x) = %016x", bits, got)
		}
	}
}
