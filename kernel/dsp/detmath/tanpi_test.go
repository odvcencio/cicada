package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// tanPiInputs: the graph filters' range (0 to 0.25), the whole period, tiny and
// large arguments, and the neighbours of quarter integers; the poles at odd
// multiples of 1/2 themselves are left out (TanPi is NaN there).
func tanPiInputs(n int) []float64 {
	var edges []float64
	for k := -16; k <= 16; k++ {
		v := float64(k) / 4
		a, b := v, v
		for i := 0; i < 4; i++ {
			a, b = math.Nextafter(a, math.Inf(-1)), math.Nextafter(b, math.Inf(1))
			edges = append(edges, a, b)
		}
		if k%2 == 0 { // not a pole
			edges = append(edges, v)
		}
	}
	return points(n, sweep(0, 0.25, n), sweep(-0.49, 0.49, n), logSweep(1e-300, 0.4, n/4), sweep(-1e6+0.1, 1e6+0.1, n), edges)
}

func TestTanPiAccuracy(t *testing.T) {
	worst, at := 0.0, 0.0
	for _, x := range tanPiInputs(sweepSize()) {
		s, c := refBigSinCos(refMul(refPi, newRef(x)))
		if c.Sign() == 0 {
			continue
		}
		ref := refQuo(s, c)
		got := TanPi(x)
		if want, _ := ref.Float64(); math.Abs(want) < 0x1p-1000 && math.Abs(got) < 0x1p-1000 {
			continue // integers: both are zero
		}
		if e := ulpError(got, ref); e > worst {
			worst, at = e, x
		}
	}
	t.Logf("TanPi: max error %.4f ULP at %v", worst, at)
	if worst > maxULP {
		t.Fatalf("TanPi(%v): error %.4f ULP exceeds %.1f", at, worst, maxULP)
	}
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0.5, -1.5} {
		if !math.IsNaN(TanPi(x)) {
			t.Errorf("TanPi(%v) = %v, want NaN", x, TanPi(x))
		}
	}
	if got := TanPi(math.Copysign(0, -1)); got != 0 || !math.Signbit(got) {
		t.Errorf("TanPi(-0) = %v, want -0", got)
	}
	if got := TanPi(0.25); got != 1 {
		t.Errorf("TanPi(0.25) = %v, want 1", got)
	}
}

// float32Sample: one of every 4,096 float32 bit patterns of each sign, plus
// zeros, infinities, the smallest subnormal and the fast paths' edges with
// their neighbours. NaN is checked apart (WebAssembly does not fix NaN bits).
func float32Sample(edges ...float32) []float32 {
	var in []float32
	for b := uint32(0); b < 0x7f800000; b += 1 << 12 {
		x := math.Float32frombits(b | 0x5a5)
		in = append(in, x, -x)
	}
	for _, e := range edges {
		for _, d := range []int32{-2, -1, 0, 1, 2} {
			x := math.Float32frombits(uint32(int32(math.Float32bits(e)) + d))
			in = append(in, x, -x)
		}
	}
	return append(in, 0, float32(math.Copysign(0, -1)), float32(math.Inf(1)), float32(math.Inf(-1)), math.SmallestNonzeroFloat32)
}

// TanPi32 and OneMinusExpNeg2Pi32 must equal their expressions bit for bit.
// These tests check a sample; the property was checked for all 2^32 float32
// inputs once (2026-11-11, amd64 and arm64: 0 differ), and the digests pin the
// sampled results on every target.
func TestTanPi32MatchesExpression(t *testing.T) {
	for _, u := range float32Sample(0x1p-30, 0.25, 0.46) {
		got, want := TanPi32(u), float32(Tan(math.Pi*float64(u)))
		if got != got && want != want {
			continue // beyond Tan's domain both are NaN
		}
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("TanPi32(%v) = %v, want %v", u, got, want)
		}
	}
	if !math.IsNaN(float64(TanPi32(float32(math.NaN())))) {
		t.Error("TanPi32(NaN) is not NaN")
	}
}

func TestOneMinusExpNeg2Pi32MatchesExpression(t *testing.T) {
	for _, u := range float32Sample(0x1p-20, 0.5) {
		got, want := OneMinusExpNeg2Pi32(u), float32(1-Exp(-2*math.Pi*float64(u)))
		if got != got && want != want {
			continue
		}
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("OneMinusExpNeg2Pi32(%v) = %v, want %v", u, got, want)
		}
	}
	if !math.IsNaN(float64(OneMinusExpNeg2Pi32(float32(math.NaN())))) {
		t.Error("OneMinusExpNeg2Pi32(NaN) is not NaN")
	}
}

// filterCoefficientDigest pins TanPi over its sweep and both float32
// functions over their samples (NaN results are hashed as one fixed word).
const filterCoefficientDigest = "c40565172f8cbca00d73eb616bacd5177a419dc68edcf9e8881457f2405f0154"

func TestFilterCoefficientDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	put := func(bits uint64) {
		binary.LittleEndian.PutUint64(word[:], bits)
		h.Write(word[:])
	}
	for _, x := range tanPiInputs(4_000) {
		v := TanPi(x)
		if v != v {
			put(0x7ff8000000000001)
			continue
		}
		put(math.Float64bits(v))
	}
	for _, f := range []func(float32) float32{TanPi32, OneMinusExpNeg2Pi32} {
		for _, u := range float32Sample(0x1p-30, 0x1p-20, 0.25, 0.46, 0.5) {
			v := f(u)
			if v != v {
				put(0x7fc00001)
				continue
			}
			put(uint64(math.Float32bits(v)))
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != filterCoefficientDigest {
		t.Fatalf("filter coefficient digest = %s, want %s", got, filterCoefficientDigest)
	}
}
