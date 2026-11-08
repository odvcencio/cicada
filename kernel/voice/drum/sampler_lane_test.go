//go:build !tinygo

package drum

import (
	"testing"

	"m31labs.dev/cicada/kernel/voice/sample"
)

// laneBank builds a pack with one note, 38, recorded only at velocities 64-127.
func laneBank(t *testing.T) *SamplerBank {
	t.Helper()
	pcm := make([]float32, 4800)
	for i := range pcm {
		pcm[i] = .5
	}
	zone := sample.Zone{Region: sample.Region{Left: pcm, SampleRate: 48_000, RootKey: 38, End: len(pcm)}, OneShot: true,
		KeyLow: 38, KeyHigh: 38, VelocityLow: 64, VelocityHigh: 127, Layer: 100, Count: 1, Gain: 1}
	inst, err := sample.NewInstrument(48_000, []sample.Zone{zone}, sample.DefaultInstrumentConfig())
	if err != nil {
		t.Fatal(err)
	}
	return NewSamplerBank(inst)
}

func TestSamplerLaneRefusesANoteThePackDoesNotCover(t *testing.T) {
	k, err := New(48_000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SetSampler(SD, laneBank(t), 40, 0, 0); err == nil {
		t.Fatal("a lane bound to an unrecorded note was accepted")
	}
}

// A soft hit below the pack's softest layer, as humanize can produce, plays
// that layer instead of faulting the whole kit.
func TestSamplerLaneClampsAVelocityBelowTheRecordedLayers(t *testing.T) {
	k, err := New(48_000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SetSampler(SD, laneBank(t), 38, 0, 0); err != nil {
		t.Fatal(err)
	}
	k.Hit(SD, 40, false)
	if k.Fault() {
		t.Fatal("a hit below the softest layer faulted the kit")
	}
	peak := float32(0)
	for i := 0; i < 480; i++ {
		l, _ := k.NextStereo()
		peak = max(peak, l)
	}
	if peak == 0 {
		t.Fatal("the clamped hit was silent")
	}
}
