package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

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
// These tests check a sample of every input; the FastPathExhaustive tests
// below check every input that takes a fast path, and the digest pins the
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

// filterCoefficientDigest pins both float32 functions over their samples
// (NaN results are hashed as one fixed word).
const filterCoefficientDigest = "8590306ace909dc77daac792e2f10f8bf3b5a1397b4f608e0c270a0b9d350419"

func TestFilterCoefficientDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	put := func(bits uint64) {
		binary.LittleEndian.PutUint64(word[:], bits)
		h.Write(word[:])
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

// exhaustive32 calls check on every float32 with bits in [lo, hi], with each
// sign when both is set, across GOMAXPROCS goroutines, and fails on the first
// three that differ and on the count.
func exhaustive32(t *testing.T, name string, lo, hi float32, both bool, f func(float32) float32, want func(float32) float32) {
	t.Helper()
	first, last := math.Float32bits(lo), math.Float32bits(hi)
	workers := runtime.GOMAXPROCS(0)
	var bad atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for b := first + uint32(w); b <= last; b += uint32(workers) {
				u := math.Float32frombits(b)
				for _, x := range [2]float32{u, -u} {
					if got, ref := f(x), want(x); math.Float32bits(got) != math.Float32bits(ref) {
						if bad.Add(1) <= 3 {
							t.Errorf("%s(%v) = %v, want %v", name, x, got, ref)
						}
					}
					if !both {
						break
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if n := bad.Load(); n > 0 {
		t.Fatalf("%s: %d fast-path inputs differ", name, n)
	}
}

// TestTanPi32FastPathExhaustive checks every float32 that takes TanPi32's fast
// path (2^-30 <= |u| <= 0.46, both signs: 483,854,912 inputs); all others go
// to Tan itself. It runs on every target CI tests (seconds on four cores).
func TestTanPi32FastPathExhaustive(t *testing.T) {
	exhaustive32(t, "TanPi32", 0x1p-30, 0.46, true, TanPi32, func(u float32) float32 { return float32(Tan(math.Pi * float64(u))) })
}

// TestOneMinusExpNeg2Pi32FastPathExhaustive checks every float32 that takes
// OneMinusExpNeg2Pi32's fast path (2^-20 <= u <= 0.5: 159,383,553 inputs);
// all others go to Exp itself.
func TestOneMinusExpNeg2Pi32FastPathExhaustive(t *testing.T) {
	exhaustive32(t, "OneMinusExpNeg2Pi32", 0x1p-20, 0.5, false, OneMinusExpNeg2Pi32, func(u float32) float32 { return float32(1 - Exp(-2*math.Pi*float64(u))) })
}
