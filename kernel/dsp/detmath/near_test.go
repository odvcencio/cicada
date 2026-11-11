package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// nearMaxULP is the bound the second-tier functions promise (package comment).
const nearMaxULP = 2.0

// tanhNearInputs adds to Tanh's sweep the branch edges (0.125, 22) and their
// neighbours, where TanhNear's error is largest.
func tanhNearInputs(n int) []float64 {
	var edges []float64
	for _, e := range []float64{0.125, 22, 0.5, 1} {
		a, b := e, e
		for i := 0; i < 8; i++ {
			a, b = math.Nextafter(a, 0), math.Nextafter(b, math.Inf(1))
			edges = append(edges, a, b, -a, -b)
		}
	}
	return points(n, sweep(-23, 23, n), sweep(-1, 1, n), logSweep(0.125, 22, n), logSweep(1e-10, 0.2, n/4), edges)
}

func TestTanhNearAccuracy(t *testing.T) {
	worst, at := 0.0, 0.0
	for _, x := range tanhNearInputs(sweepSize()) {
		if e := ulpError(TanhNear(x), refTanh(x)); e > worst {
			worst, at = e, x
		}
	}
	t.Logf("TanhNear: max error %.4f ULP at %v", worst, at)
	if worst > nearMaxULP {
		t.Fatalf("TanhNear(%v): error %.4f ULP exceeds %.1f", at, worst, nearMaxULP)
	}
	for _, x := range []float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1)} {
		if got, want := TanhNear(x), Tanh(x); math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("TanhNear(%v) = %v, want %v", x, got, want)
		}
	}
	if !math.IsNaN(TanhNear(math.NaN())) {
		t.Error("TanhNear(NaN) is not NaN")
	}
}

func TestSinPiNearAccuracy(t *testing.T) {
	worst, at := 0.0, 0.0
	for _, x := range sinPiInputs(sweepSize()) {
		s, _ := refBigSinCos(refMul(refPi, newRef(x)))
		got := SinPiNear(x)
		if want, _ := s.Float64(); math.Abs(want) < 0x1p-1000 && math.Abs(got) < 0x1p-1000 {
			continue // integers: both are zero
		}
		if e := ulpError(got, s); e > worst {
			worst, at = e, x
		}
	}
	t.Logf("SinPiNear: max error %.4f ULP at %v", worst, at)
	if worst > nearMaxULP {
		t.Fatalf("SinPiNear(%v): error %.4f ULP exceeds %.1f", at, worst, nearMaxULP)
	}
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if !math.IsNaN(SinPiNear(x)) {
			t.Errorf("SinPiNear(%v) is not NaN", x)
		}
	}
	if got := SinPiNear(math.Copysign(0, -1)); got != 0 || !math.Signbit(got) {
		t.Errorf("SinPiNear(-0) = %v, want -0", got)
	}
}

// nearDigest pins the bits of both second-tier functions over fixed sweeps;
// CI requires the same digest on arm64 and with GOAMD64=v3.
const nearDigest = "469349933654dbedc4a18c93f4cb31b9ccbb6be2c2a05bb2f1e603cf79a80aed"

// SinPiNear gives SinPi's signed zero at every integer, including those where
// 2*x would overflow.
func TestSinPiNearIntegers(t *testing.T) {
	for _, x := range []float64{1, -1, 2, -2, 3, 0x1p52 + 1, -0x1p52 - 1, 0x1p53, 0x1p1023, -0x1p1023, math.MaxFloat64, -math.MaxFloat64, 0x1.8p1022} {
		if got, want := SinPiNear(x), SinPi(x); got != 0 || math.Signbit(got) != math.Signbit(want) {
			t.Errorf("SinPiNear(%v) = %v, want %v", x, got, want)
		}
	}
}

func TestNearDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	put := func(v float64) {
		binary.LittleEndian.PutUint64(word[:], math.Float64bits(v))
		h.Write(word[:])
	}
	for _, x := range tanhNearInputs(4_000) {
		put(TanhNear(x))
	}
	for _, x := range sinPiInputs(4_000) {
		put(SinPiNear(x))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != nearDigest {
		t.Fatalf("near digest = %s, want %s", got, nearDigest)
	}
}
