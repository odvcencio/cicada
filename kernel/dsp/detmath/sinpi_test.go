package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// sinPiInputs covers whole turns, tiny and large arguments, and the doubles
// next to every quarter and half integer in [-8, 8], where the reduction
// switches quadrant.
func sinPiInputs(n int) []float64 {
	var edges []float64
	for k := -32; k <= 32; k++ {
		v := float64(k) / 4
		a, b := v, v
		for i := 0; i < 4; i++ {
			a, b = math.Nextafter(a, math.Inf(-1)), math.Nextafter(b, math.Inf(1))
			edges = append(edges, a, b)
		}
	}
	return points(n, sweep(-4, 4, n), sweep(-1e6, 1e6, n), logSweep(1e-300, 1, n/4), logSweep(1, 0x1p53, n/4), edges)
}

func TestSinPiAccuracy(t *testing.T) {
	worst, at := 0.0, 0.0
	for _, x := range sinPiInputs(sweepSize()) {
		s, _ := refBigSinCos(refMul(refPi, newRef(x)))
		got := SinPi(x)
		if want, _ := s.Float64(); math.Abs(want) < 0x1p-1000 && math.Abs(got) < 0x1p-1000 {
			continue // integers: both are zero (the reference rounds to 0)
		}
		if e := ulpError(got, s); e > worst {
			worst, at = e, x
		}
	}
	t.Logf("SinPi: max error %.4f ULP at %v", worst, at)
	if worst > maxULP {
		t.Fatalf("SinPi(%v): error %.4f ULP exceeds %.1f", at, worst, maxULP)
	}
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if !math.IsNaN(SinPi(x)) {
			t.Errorf("SinPi(%v) is not NaN", x)
		}
	}
	if got := SinPi(math.Copysign(0, -1)); got != 0 || !math.Signbit(got) {
		t.Errorf("SinPi(-0) = %v, want -0", got)
	}
	for _, c := range []struct{ x, want float64 }{{0.5, 1}, {-0.5, -1}, {1.5, -1}, {2.5, 1}, {1e300, 0}} {
		if got := SinPi(c.x); got != c.want {
			t.Errorf("SinPi(%v) = %v, want %v", c.x, got, c.want)
		}
	}
}

// sinPiDigest pins the bits of SinPi over a fixed sweep; CI requires the same
// digest on arm64 and with GOAMD64=v3.
const sinPiDigest = "853a5279a8203f7bbd64ed6be03cffe740f0886407c27cc626cb9bff5759b3f4"

func TestSinPiDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	for _, x := range sinPiInputs(4_000) {
		binary.LittleEndian.PutUint64(word[:], math.Float64bits(SinPi(x)))
		h.Write(word[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sinPiDigest {
		t.Fatalf("sinpi digest = %s, want %s", got, sinPiDigest)
	}
}
