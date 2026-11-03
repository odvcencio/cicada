package fastmath

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestExp2FinitePreservesReferenceBits(t *testing.T) {
	h := sha256.New()
	var buf [8]byte
	for i := 0; i <= 100_000; i++ {
		x := -32 + 64*float64(i)/100_000
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(Exp2Finite(x)))
		_, _ = h.Write(buf[:])
	}
	const want = "480e2e0b6d59c3792310850fceb1427881e8c119e48921b1e67d44b9432e4444"
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Fatalf("finite exp2 changed reference bits: %s", got)
	}
}

func TestExp2GridAndAnchors(t *testing.T) {
	if math.Float64bits(Exp2(0)) != math.Float64bits(1) || math.Float64bits(Exp2(1)) != math.Float64bits(2) {
		t.Fatal("exp2 integer anchors are not exact")
	}
	maxRelative := 0.0
	for i := 0; i <= 100_000; i++ {
		x := -32 + 64*float64(i)/100_000
		want := math.Exp2(x)
		relative := math.Abs(Exp2(x)-want) / want
		if relative > maxRelative {
			maxRelative = relative
		}
	}
	if maxRelative > 1e-6 {
		t.Fatalf("exp2 max relative error %g", maxRelative)
	}
}

func TestExp2SpecialInputs(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{math.NaN(), 0}, {math.Inf(-1), Exp2Finite(-32)},
		{math.Inf(1), Exp2Finite(32)}, {math.Copysign(0, -1), 1},
	} {
		if got := Exp2(tc.in); math.Float64bits(got) != math.Float64bits(tc.want) {
			t.Fatalf("Exp2(%g) = %g, want %g", tc.in, got, tc.want)
		}
	}
}

func TestLog2GridAndAnchors(t *testing.T) {
	if Log2(1) != 0 || Log2(2) != 1 {
		t.Fatal("log2 integer anchors are not exact")
	}
	maxError := 0.0
	for i := 0; i <= 100_000; i++ {
		x := math.Exp2(-32 + 64*float64(i)/100_000)
		err := math.Abs(Log2(x) - math.Log2(x))
		if err > maxError {
			maxError = err
		}
	}
	if maxError > 3e-8 {
		t.Fatalf("log2 maximum absolute error %g", maxError)
	}
}

func TestTanhGridAndAnchor(t *testing.T) {
	if math.Float64bits(Tanh(0)) != 0 {
		t.Fatal("tanh zero anchor is not exact")
	}
	maxError := 0.0
	for i := 0; i <= 100_000; i++ {
		x := -3 + 6*float64(i)/100_000
		err := math.Abs(Tanh(x) - math.Tanh(x))
		if err > maxError {
			maxError = err
		}
	}
	if maxError > 1e-6 {
		t.Fatalf("tanh max error %g", maxError)
	}
	if Tanh(100) > 1 || Tanh(-100) < -1 {
		t.Fatal("tanh saturation exceeded unity")
	}
}

func TestTanSmallCoefficientRange(t *testing.T) {
	maxError := 0.0
	for i := 0; i <= 100_000; i++ {
		x := -.45*math.Pi/2 + .45*math.Pi*float64(i)/100_000
		err := math.Abs(TanSmall(x) - math.Tan(x))
		if err > maxError {
			maxError = err
		}
	}
	if maxError > 1e-6 {
		t.Fatalf("tan coefficient max error %g", maxError)
	}
}

func TestFastMathDoesNotAllocate(t *testing.T) {
	if n := testing.AllocsPerRun(1000, func() {
		Exp2(0.23)
		Tanh(0.72)
		TanSmall(0.19)
	}); n != 0 {
		t.Fatalf("fast math allocated %v times", n)
	}
}
