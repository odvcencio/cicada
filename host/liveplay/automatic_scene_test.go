package liveplay

import (
	"encoding/binary"
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
)

// previewBarBlocks is the number of render blocks in one bar of
// previewRenderScore (20 BPM at 48 kHz: 576000 frames).
const previewBarBlocks = 576_000 / blockFrames

func lastBlockPeak(t *testing.T, p *Player, blocks int) float64 {
	t.Helper()
	pcm := make([]byte, blockFrames*8)
	for i := 0; i < blocks; i++ {
		if _, err := io.ReadFull(p, pcm); err != nil {
			t.Fatal(err)
		}
	}
	peak := 0.0
	for frame := 0; frame < blockFrames; frame++ {
		peak = max(peak, math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8:])))))
	}
	return peak
}

// The song enters scene "later" (mix gain -9 dB) at the start of bar 2. A
// patch can land in the block before that boundary, in the boundary block, or
// after it. The render thread applies the patch first in the first two cases
// and the scene second, so the scene value must win everywhere. In the third
// case the patch is last and must win.
func TestAutomaticSceneTransitionOrdersCommittedValueWithEngine(t *testing.T) {
	const scene, patched = -9, -3
	for name, test := range map[string]struct {
		blocksBeforePatch int
		want              float64
	}{
		"patch before the boundary block": {previewBarBlocks - 1, scene},
		"patch in the boundary block":     {previewBarBlocks, scene},
		"patch after the boundary block":  {previewBarBlocks + 1, patched},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := New(previewRenderScore(t, -6), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if err := p.StartSongEntry(0, "preview"); err != nil {
				t.Fatal(err)
			}
			lastBlockPeak(t, p, test.blocksBeforePatch)
			target, _ := p.ResolvePreviewTrack("bass")
			arena := NewPatchArena(1)
			batch := arena.Begin(1)
			batch.SetResolvedParam(target, kernel.ParamMixGain, patched)
			if err := p.Patch(batch); err != nil {
				t.Fatal(err)
			}
			lastBlockPeak(t, p, 1)
			if test.blocksBeforePatch == previewBarBlocks-1 {
				lastBlockPeak(t, p, 1) // The boundary block itself.
			}
			wantPeak := .125 * math.Pow(10, test.want/20)
			if got := lastBlockPeak(t, p, 64); math.Abs(got-wantPeak) > .002 {
				t.Fatalf("engine peak %g, want %g (%g dB)", got, wantPeak, test.want)
			}
			if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || float64(value) != test.want {
				t.Fatalf("committed value %g/%v, engine plays %g", value, ok, test.want)
			}
			version, err := p.SetResolvedPreview(target, kernel.ParamMixGain, 0)
			if err != nil {
				t.Fatal(err)
			}
			lastBlockPeak(t, p, 64)
			p.CancelPreviewWait(version)
			wantPeak = .125 * math.Pow(10, test.want/20)
			if got := lastBlockPeak(t, p, 64); math.Abs(got-wantPeak) > .002 {
				t.Fatalf("after cancel peak %g, want %g (%g dB)", got, wantPeak, test.want)
			}
		})
	}
}
