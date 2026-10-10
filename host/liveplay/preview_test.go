package liveplay

import (
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

func renderPreview(t *testing.T, p *Player, frames int64) {
	t.Helper()
	if _, err := io.CopyN(io.Discard, p, frames*8); err != nil {
		t.Fatal(err)
	}
}

func assertPreviewGain(t *testing.T, p *Player, gain float64) {
	t.Helper()
	select {
	case frame := <-p.Meters():
		want := math.Pow(10, gain/20)
		if math.Abs(float64(frame.Tracks[0].Peak)-want) > .01 {
			t.Fatalf("measured peak %g, want %g (%g dB)", frame.Tracks[0].Peak, want, gain)
		}
	default:
		t.Fatal("no playback meter")
	}
}

func TestPreviewCancelRestoresCommittedAudioAndRetiresOverride(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	version, err := p.SetPreview(0, kernel.ParamMixGain, 0)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 24_064)
	assertPreviewGain(t, p, 0)
	if err := p.CancelPreview(version); err != nil {
		t.Fatal(err)
	}
	// Retirement and restoration are applied at the next block boundary.
	renderPreview(t, p, blockFrames)
	if value, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatalf("cancel left override %g active", value)
	}
	renderPreview(t, p, 24_064)
	assertPreviewGain(t, p, -6)
}

func TestPreviewStaleQueuedCancelPreservesNewerGesture(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a, err := p.SetPreview(0, kernel.ParamMixGain, 0)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if err := p.CancelPreview(a); err != nil {
		t.Fatal(err)
	}
	b, err := p.SetPreview(0, kernel.ParamMixGain, -9)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 24_064)
	if value, active := p.OverrideValue(0, kernel.ParamMixGain); !active || value != -9 {
		t.Fatalf("stale cancel cleared newer gesture: %g, %v", value, active)
	}
	assertPreviewGain(t, p, -9)
	if err := p.CancelPreview(b); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 24_064)
	assertPreviewGain(t, p, -6)
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("newer gesture was not retired")
	}
}

func TestPreviewCancellationDoesNotAllocateOnTheRenderThread(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Prepare a distinct control snapshot for the warm-up and each measured
	// iteration, so every render drains and applies a real cancellation.
	var snapshots [101]*liveOverrides
	var versions [101]uint64
	for i := range snapshots {
		versions[i], err = p.SetPreview(0, kernel.ParamMixGain, 0)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[i] = p.overrides.Load()
	}
	var output [blockFrames * 8]byte
	var cancelErr, readErr error
	iteration := 0
	allocs := testing.AllocsPerRun(100, func() {
		p.overrides.Store(snapshots[iteration])
		cancelErr = p.CancelPreview(versions[iteration])
		_, readErr = p.Read(output[:])
		iteration++
	})
	if cancelErr != nil || readErr != nil {
		t.Fatalf("cancel/read: %v, %v", cancelErr, readErr)
	}
	if allocs != 0 {
		t.Fatalf("cancellation allocated %g times per render", allocs)
	}
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("measured cancellation did not retire the override")
	}
	assertPreviewGain(t, p, -6)
}

func TestPreviewCancelRetriesWhenTheEngineQueueIsFull(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	version, err := p.SetPreview(0, kernel.ParamMixGain, 0)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 24_064)
	// This test owns Read and the engine queue on the same goroutine.
	for p.current.Engine.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(0)}) {
	}
	if err := p.CancelPreview(version); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); !active {
		t.Fatal("override retired before its restoration command was accepted")
	}
	renderPreview(t, p, blockFrames)
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("cancellation was lost when the engine queue was full")
	}
	renderPreview(t, p, 24_064)
	assertPreviewGain(t, p, -6)
}
