package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
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
