package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"testing"
)

// refLog1p sums the series x - x^2/2 + x^3/3 - ... for |x| < 1/4, where
// 1 + x would lose x at the reference precision, and takes the logarithm of
// 1 + x above.
func refLog1p(x float64) *big.Float {
	if math.Abs(x) >= 0.25 {
		return refBigLog(refAdd(refInt(1), newRef(x)))
	}
	v := newRef(x)
	sum, power := refAdd(v, refInt(0)), refAdd(v, refInt(0))
	for k := int64(2); ; k++ {
		power = refMul(power, v)
		term := refQuo(power, refInt(k))
		if k%2 == 0 {
			sum = refSub(sum, term)
		} else {
			sum = refAdd(sum, term)
		}
		if new(big.Float).Abs(term).Cmp(new(big.Float).Mul(new(big.Float).Abs(sum), refEps)) < 0 {
			return sum
		}
	}
}

func log1pInputs(n int) []float64 {
	var edges []float64
	for _, e := range []float64{0x1p-54, -0x1p-54, 0x1p-53, -1 + 0x1p-53, 1, -0.5} {
		a, b := e, e
		for i := 0; i < 4; i++ {
			a, b = math.Nextafter(a, math.Inf(-1)), math.Nextafter(b, math.Inf(1))
			edges = append(edges, a, b)
		}
	}
	neg := logSweep(1e-300, 0.999999, n)
	for i := range neg {
		neg[i] = -neg[i]
	}
	// sweep(0, 1, n): e^(-2|x|), the arguments the guitar amp passes (logcosh).
	return points(n, logSweep(1e-300, 1e300, n), neg, sweep(-0.9, 10, n), sweep(0, 1, n), edges)
}

func TestLog1pAccuracy(t *testing.T) {
	worst, at := 0.0, 0.0
	for _, x := range log1pInputs(sweepSize()) {
		if x <= -1 {
			continue
		}
		if e := ulpError(Log1p(x), refLog1p(x)); e > worst {
			worst, at = e, x
		}
	}
	t.Logf("Log1p: max error %.4f ULP at %v", worst, at)
	const claimed = 0.80
	if worst > claimed {
		t.Fatalf("Log1p(%v): error %.4f ULP exceeds the claimed %.2f", at, worst, claimed)
	}
	if !math.IsNaN(Log1p(-1.5)) || !math.IsInf(Log1p(-1), -1) || !math.IsInf(Log1p(math.Inf(1)), 1) {
		t.Error("Log1p special values")
	}
	if got := Log1p(math.Copysign(0, -1)); got != 0 || !math.Signbit(got) {
		t.Errorf("Log1p(-0) = %v, want -0", got)
	}
}

// log1pDigest was pinned 2026-11-11 when Log1p was added (#329), and moved
// the same day when Log1p changed from a double-double logarithm to the
// rounded fdlibm algorithm (six times faster, 0.80 ULP instead of 0.65) and
// its inputs gained sweep(0, 1, n). The amd64 value; CI requires it on arm64,
// with GOAMD64=v3 and under TinyGo WebAssembly.
const log1pDigest = "9a5c2d79387eb2294d9da83ea5658665b4a5a9f3b0331597aede90ec0339c36e"

func TestLog1pDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	for _, x := range log1pInputs(4_000) {
		if x <= -1 {
			continue
		}
		binary.LittleEndian.PutUint64(word[:], math.Float64bits(Log1p(x)))
		h.Write(word[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != log1pDigest {
		t.Fatalf("log1p digest = %s, want %s", got, log1pDigest)
	}
}
