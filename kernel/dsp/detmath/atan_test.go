package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"testing"
)

// refAtan returns atan(x) to refPrec bits: atan(x) = pi/2 - atan(1/x) for
// |x| > 1, then atan(x) = 2 atan(x / (1 + sqrt(1 + x^2))) until |x| < 1/16,
// then the Taylor series.
func refAtan(x float64) *big.Float {
	if math.IsInf(x, 0) {
		return refMul(refQuo(refPi, refInt(2)), newRef(math.Copysign(1, x)))
	}
	v := newRef(x)
	negative := v.Sign() < 0
	v.Abs(v)
	invert := v.Cmp(refInt(1)) > 0
	if invert {
		v = refQuo(refInt(1), v)
	}
	doublings := 0
	for v.Cmp(refQuo(refInt(1), refInt(16))) > 0 {
		root := new(big.Float).SetPrec(refPrec).Sqrt(refAdd(refInt(1), refMul(v, v)))
		v = refQuo(v, refAdd(refInt(1), root))
		doublings++
	}
	sum, power, v2 := refAdd(v, refInt(0)), refAdd(v, refInt(0)), refMul(v, v)
	for k := int64(3); ; k += 2 {
		power = refMul(power, v2)
		term := refQuo(power, refInt(k))
		if k%4 == 3 {
			sum = refSub(sum, term)
		} else {
			sum = refAdd(sum, term)
		}
		if small(term) {
			break
		}
	}
	for ; doublings > 0; doublings-- {
		sum = refMul(sum, refInt(2))
	}
	if invert {
		sum = refSub(refQuo(refPi, refInt(2)), sum)
	}
	if negative {
		sum.Neg(sum)
	}
	return sum
}

// atanInputs covers both reduction boundaries (0.66 and tan(3pi/8)), tiny and
// huge arguments, and the neighbours of the boundaries.
func atanInputs(n int) []float64 {
	var edges []float64
	for _, b := range []float64{0.66, 2.41421356237309504880, 1} {
		v := b
		for i := 0; i < 8; i++ {
			edges = append(edges, v, math.Nextafter(b, math.Inf(1)))
			v = math.Nextafter(v, 0)
		}
	}
	return points(n, sweep(-10, 10, n), sweep(-1, 1, n), logSweep(1e-300, 1e300, n/2), sweep(0.6, 2.5, n/4), edges)
}

func TestAtanAccuracy(t *testing.T) {
	worst, at := 0.0, 0.0
	for _, x := range atanInputs(sweepSize()) {
		if e := ulpError(Atan(x), refAtan(x)); e > worst {
			worst, at = e, x
		}
	}
	t.Logf("Atan: max error %.4f ULP at %v", worst, at)
	// The bound the doc comment claims (measured on the dense sweep).
	const claimed = 0.83
	if worst > claimed {
		t.Fatalf("Atan(%v): error %.4f ULP exceeds the claimed %.2f", at, worst, claimed)
	}
	for _, x := range []float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1)} {
		if got, want := Atan(x), math.Atan(x); math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("Atan(%v) = %v, want %v", x, got, want)
		}
	}
	if !math.IsNaN(Atan(math.NaN())) {
		t.Error("Atan(NaN) is not NaN")
	}
}

// atanDigest pins the bits of Atan over a fixed sweep; CI requires the same
// digest on arm64 and with GOAMD64=v3. Pinned 2026-11-10 when Atan was added
// (#328): the amd64 value, equal under WebAssembly (js/wasm) and on arm64.
const atanDigest = "727c91878985f9966d6826c03a927545430abb6100d79ed28011342d11b50e9a"

func TestAtanDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	for _, x := range atanInputs(4_000) {
		binary.LittleEndian.PutUint64(word[:], math.Float64bits(Atan(x)))
		h.Write(word[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != atanDigest {
		t.Fatalf("atan digest = %s, want %s", got, atanDigest)
	}
}
