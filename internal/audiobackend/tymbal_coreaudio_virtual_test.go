//go:build darwin

package audiobackend

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
)

// This opt-in test uses a named virtual device and never stores input audio.
// It exercises the same callback and capture ring as Studio at device speed.
func TestCoreAudioVirtualCaptureContinuity(t *testing.T) {
	name := os.Getenv("CICADA_VIRTUAL_AUDIO_DEVICE")
	if name == "" {
		t.Skip("set CICADA_VIRTUAL_AUDIO_DEVICE to an available virtual duplex device")
	}
	b, err := New(Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	access, err := QueryInputAccess(b)
	if err == nil && access == InputAccessPrompt && os.Getenv("CICADA_REQUEST_AUDIO_ACCESS") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err = RequestInputAccess(ctx, b); err == nil {
			access, err = QueryInputAccess(b)
		}
	}
	if err != nil || access != InputAccessGranted {
		t.Fatalf("input access %q: %v", access, err)
	}
	devices, supported, err := b.Devices()
	if err != nil || !supported {
		t.Fatalf("device enumeration: %v", err)
	}
	var selected Device
	for _, d := range devices {
		if d.Name == name && d.Inputs >= 2 && d.Outputs >= 2 {
			selected = d
			break
		}
	}
	if selected.ID == "" {
		t.Fatalf("virtual duplex device %q is unavailable", name)
	}
	var blocks, frames, discontinuities atomic.Uint64
	recorder, err := capture.NewRecorder(256, 2048, 2, func(block capture.RecordedBlock, _ [][]float32) error {
		blocks.Add(1)
		frames.Add(uint64(block.Timing.Frames))
		if block.Timing.Flags != 0 || block.Timing.GapFrames != 0 {
			discontinuities.Add(1)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if err := recorder.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Channels: 2, CaptureChannels: 2, Device: selected.ID, CaptureDevice: selected.ID, FramesPerPeriod: 256, Capture: recorder.Capture}
	cfg.SampleRate, err = b.SampleRate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := b.Open(cfg, func(_ [][]float32, output [][]float32) error {
		for _, ch := range output {
			clear(ch)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err = stream.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Second)
	stream.Pause()
	recorder.End()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, stats := recorder.Snapshot(), stream.Stats()
	t.Logf("device=%q format=%+v callbacks=%d blocks=%d frames=%d discontinuities=%d ring=%+v incomplete=%v", name, stream.Format(), stats.Callbacks, blocks.Load(), frames.Load(), discontinuities.Load(), snapshot.Ring, snapshot.Incomplete)
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if blocks.Load() == 0 || frames.Load() == 0 || discontinuities.Load() != 0 || snapshot.Incomplete {
		t.Fatal("virtual capture was incomplete")
	}
}
