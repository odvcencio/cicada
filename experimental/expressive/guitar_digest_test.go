package expressive

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"testing"
)

// guitarDigest pins the guitar's float64 output bits: three expressions at
// three sample rates, each with a smoothed control change and a release, 40,000
// samples apiece (360,000 in all). The voice and its amp use kernel/dsp/detmath,
// so the digest is the same on amd64 and arm64; the WebAssembly kernel's guitar
// is compared bit for bit with native by TestAudioWASMGuitarSampleParity
// (cmd/cicada-kernel-wasm). Set on 2026-11-12 for #343, whose faster Log1p (at
// most 0.80 ULP, was 0.65) changed the amp's logcosh and so these samples,
// while no example WAV render moved; the next change to the guitar's bits shows
// here. Print a new value with CICADA_GUITAR_DIGEST_PRINT=1.
const guitarDigest = "dcabc72fa8066dc5ad4a34235221918ab0186865792aaac4e0c4ab61adfd5100"

func TestGuitarDigest(t *testing.T) {
	h := sha256.New()
	var w [8]byte
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, e := range []Expression{
			{PitchHz: 110, Position: .22, Brightness: .7},
			{PitchHz: 220, Position: .3, Brightness: .4, Vibrato: 25, Damping: .3, Drive: .8},
			{PitchHz: 440, Position: .1, Brightness: .9, Vibrato: 0, Damping: 0, Drive: 1},
		} {
			g := NewGuitar(rate)
			g.SetExpressionImmediate(e)
			g.NoteOn(e.PitchHz, .8)
			for i := 0; i < 40_000; i++ {
				if i == 15_000 { // a bend up a fifth, with vibrato and drive moving
					next := e
					next.PitchHz *= 1.5
					next.Vibrato = 10 - e.Vibrato/2
					next.Drive = .3
					g.SetExpression(next)
				}
				if i == 30_000 {
					g.NoteOff()
				}
				binary.LittleEndian.PutUint64(w[:], math.Float64bits(g.Next()))
				h.Write(w[:])
			}
		}
	}
	got := hex.EncodeToString(h.Sum(nil))
	if os.Getenv("CICADA_GUITAR_DIGEST_PRINT") == "1" {
		t.Logf("guitarDigest = %q", got)
		return
	}
	if got != guitarDigest {
		t.Fatalf("guitar digest = %s, want %s", got, guitarDigest)
	}
}
