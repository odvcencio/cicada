//go:build browser

package main

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestBrowserUnifiedMixedParity(t *testing.T) {
	wave := testwav.Bytes(48000, 1, 16, 512, 1)
	source := []byte(fmt.Sprintf(`cicada 2
tempo 120
asset resident "resident.wav" { sha256="%x" format=wav frames=512 rate=48000Hz channels=1 }
clip click resident { start=0ms end=10ms }
sampler sample-key { asset=resident root=c4 mode=loop voices=2 }
instrument piano { voice poly { out=sine(pitch)*env(gate,100ms)*0.1 } }
track keys piano {}
track sampled sample-key {}
track audio audio {}
pattern harmony notes { [c4 e4 g4] - . [d4 f4 a4] }
pattern melody { c4 . c4 - }
arrange {
 place chords keys harmony { at=0ticks length=1bar }
 place notes sampled melody { at=0ticks length=1bar }
 place click-part audio click { at=0ticks length=1bar }
}
`, sha256.Sum256(wave)))
	score, ds := notation.Parse(source)
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	pcm := make([]float32, 512)
	for i := range pcm {
		pcm[i] = float32(i%256-128) / 1024
	}
	cfg, err := project.CompileEngineWithAssets(p, 48000, 128, []engine.AudioAsset{{SampleRate: 48000, Left: pcm}})
	if err != nil {
		t.Fatal(err)
	}
	reference := nativeConfigReference(t, cfg, 2)
	checkBrowserParity(t, source, reference, map[string][]byte{"resident.wav": wave}, "synthetic graph chords + sampler + scheduled resident clip", 2, "browser-unified-parity-report.json")
}
