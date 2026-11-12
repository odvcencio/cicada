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

// tanh32Inputs: every float32 in [-12, 12] with the low 12 mantissa bits
// fixed, so each binade is sampled evenly, plus infinities, signed zeros,
// subnormals and the fast path's edges (2^-12 and 9) with their neighbours.
func tanh32Inputs() []float32 {
	var in []float32
	for b := uint32(0); b < 0x41400000; b += 1 << 12 { // 0 to 12
		x := math.Float32frombits(b | 0x5a5)
		in = append(in, x, -x)
	}
	for _, e := range []float32{0x1p-12, 9} {
		for _, d := range []int32{-2, -1, 0, 1, 2} {
			x := math.Float32frombits(uint32(int32(math.Float32bits(e)) + d))
			in = append(in, x, -x)
		}
	}
	return append(in, float32(math.Inf(1)), float32(math.Inf(-1)), 0, float32(math.Copysign(0, -1)), math.SmallestNonzeroFloat32)
}

// Tanh32 must equal float32(Tanh(float64(x))) bit for bit. This test checks
// one of every 4,096 float32 values in [-12, 12] (both signs); the property was checked for
// all 2^32 float32 inputs once (2026-11-10: 0 differ on arm64 and on amd64), and the digest below pins
// the sampled results on every target.
func TestTanh32MatchesTanh(t *testing.T) {
	n := 0
	for _, x := range tanh32Inputs() {
		got, want := Tanh32(x), float32(Tanh(float64(x)))
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("Tanh32(%v) = %v, want float32(Tanh) = %v", x, got, want)
		}
		n++
	}
	if !math.IsNaN(float64(Tanh32(float32(math.NaN())))) {
		t.Error("Tanh32(NaN) is not NaN")
	}
	t.Logf("%d inputs equal", n)
}

// tanh32Digest was pinned 2026-11-10 when Tanh32 was added (#328): the amd64
// value, equal under WebAssembly (js/wasm) and on arm64.
const tanh32Digest = "4205ccf28fa29606418446bbe2ed86f8af5794cc70da4ecd344ed8c18b9f4350"

func TestTanh32Digest(t *testing.T) {
	h := sha256.New()
	var word [4]byte
	for _, x := range tanh32Inputs() {
		binary.LittleEndian.PutUint32(word[:], math.Float32bits(Tanh32(x)))
		h.Write(word[:])
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != tanh32Digest {
		t.Fatalf("tanh32 digest = %s, want %s", got, tanh32Digest)
	}
}

// TestTanh32FastPathExhaustive checks every float32 that takes Tanh32's fast
// path (2^-12 <= |x| < 9, both signs: 253,755,392 inputs); all others go to
// Tanh itself. It runs when CICADA_DETMATH_EXHAUSTIVE=1 (skipUnlessExhaustive).
func TestTanh32FastPathExhaustive(t *testing.T) {
	skipUnlessExhaustive(t)
	lo, hi := math.Float32bits(0x1p-12), math.Float32bits(9)
	workers := runtime.GOMAXPROCS(0)
	var bad atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for b := lo + uint32(w); b < hi; b += uint32(workers) {
				for _, x := range [2]float32{math.Float32frombits(b), -math.Float32frombits(b)} {
					if got, want := Tanh32(x), float32(Tanh(float64(x))); math.Float32bits(got) != math.Float32bits(want) {
						if bad.Add(1) <= 3 {
							t.Errorf("Tanh32(%v) = %v, want %v", x, got, want)
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if n := bad.Load(); n > 0 {
		t.Fatalf("%d of %d fast-path inputs differ", n, 2*int64(hi-lo))
	}
}
