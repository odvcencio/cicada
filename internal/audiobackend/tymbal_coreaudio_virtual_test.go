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

// This separate opt-in probe sends a known low-level marker to the named
// virtual output and checks captured energy in memory. It requires a virtual
// device that actually loops its output back to its input; silence is not a
// successful fidelity result. It does not persist input samples.
func TestCoreAudioVirtualSignalFidelity(t *testing.T) {
	name := os.Getenv("CICADA_VIRTUAL_AUDIO_DEVICE")
	if name == "" || os.Getenv("CICADA_CAPTURE_SIGNAL") != "1" {
		t.Skip("set CICADA_VIRTUAL_AUDIO_DEVICE and CICADA_CAPTURE_SIGNAL=1 for a virtual loopback signal probe")
	}
	runCoreAudioSignalFidelity(t, name, name, 2, false)
}

// The physical probe is independently opt-in because it emits an audible
// low-level marker through the specifically named output device.
func TestCoreAudioPhysicalSignalFidelity(t *testing.T) {
	output, input := os.Getenv("CICADA_SIGNAL_OUTPUT"), os.Getenv("CICADA_SIGNAL_INPUT")
	if os.Getenv("CICADA_CAPTURE_PHYSICAL_SIGNAL") != "1" || output == "" || input == "" {
		t.Skip("set CICADA_CAPTURE_PHYSICAL_SIGNAL=1 and name the input and output devices")
	}
	runCoreAudioSignalFidelity(t, output, input, 1, true)
}

func runCoreAudioSignalFidelity(t *testing.T, outputName, inputName string, channels int, acoustic bool) {
	t.Helper()
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
	var output, input Device
	for _, d := range devices {
		if d.Name == outputName && d.Outputs >= 2 {
			output = d
		}
		if d.Name == inputName && d.Inputs >= channels {
			input = d
		}
	}
	if output.ID == "" || input.ID == "" {
		t.Fatalf("requested output %q or input %q is unavailable", outputName, inputName)
	}
	cfg := Config{Channels: 2, CaptureChannels: channels, Device: output.ID, CaptureDevice: input.ID, FramesPerPeriod: 256}
	cfg.SampleRate, err = b.SampleRate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SampleRate < 8000 || cfg.SampleRate%100 != 0 {
		t.Fatal("signal probe needs a sample rate divisible by 100")
	}
	signal := newCaptureSignal(cfg.SampleRate, 12)
	if acoustic {
		signal.high, signal.low = .04, .0033
	}
	recorder, err := capture.NewRecorder(256, 2048, channels, func(_ capture.RecordedBlock, input [][]float32) error {
		signal.input(input)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if err := recorder.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	cfg.Capture = recorder.Capture
	stream, err := b.Open(cfg, func(_ [][]float32, output [][]float32) error { signal.output(output); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(12 * time.Second)
	stream.Pause()
	recorder.End()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	corr, rms, windows, silent := signal.match()
	snapshot := recorder.Snapshot()
	t.Logf("output=%q input=%q rate=%d captured=%d correlation=%.3f high_rms=%.6f high_windows=%d silent_high=%d incomplete=%v ring=%+v", outputName, inputName, cfg.SampleRate, snapshot.WrittenFrames, corr, rms, windows, silent, snapshot.Incomplete, snapshot.Ring)
	if snapshot.Incomplete {
		t.Fatal("capture reported a discontinuity, overrun, or writer fault")
	}
	minimumRMS := .001
	if acoustic {
		minimumRMS = .00005
	}
	if corr < .7 || rms < minimumRMS || windows < 20 {
		t.Fatal("the input did not contain a recognizable marker; confirm the device routing and signal level")
	}
	if silent != 0 {
		t.Fatal("the captured marker contains unexpectedly silent high-level windows")
	}
}
