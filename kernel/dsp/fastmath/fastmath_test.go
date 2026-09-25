package fastmath

import (
	"math"
	"testing"
)

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
