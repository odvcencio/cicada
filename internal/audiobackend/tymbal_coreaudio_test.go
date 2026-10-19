//go:build darwin

package audiobackend

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoreAudioCaptureDiscoveryDoesNotRequestPermission(t *testing.T) {
	b, err := New(Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	if !b.CaptureSupported() {
		t.Fatal("native Core Audio capture is unavailable")
	}
	access, err := QueryInputAccess(b)
	if err != nil || access == "" {
		t.Fatalf("read-only input permission query: %q %v", access, err)
	}
	cfg := Config{SampleRate: 48000, Channels: 2, FramesPerPeriod: 256, CaptureChannels: 1, Device: "not-a-device"}
	if _, err := b.SampleRate(cfg); err == nil || strings.Contains(err.Error(), "not supported") {
		t.Fatalf("capture rate query did not validate the selected device: %v", err)
	}
	if _, err := b.Open(cfg, func(_, _ [][]float32) error { return nil }); err == nil || strings.Contains(err.Error(), "not supported") {
		t.Fatalf("capture open did not validate the selected device: %v", err)
	}
}

// An explicit hardware gate: render silence through the same adapter Studio
// uses, including selected-device lookup, period negotiation and teardown.
func TestCoreAudioSelectedDevicePlayback(t *testing.T) {
	if os.Getenv("CICADA_COREAUDIO_TEST") != "1" {
		t.Skip("set CICADA_COREAUDIO_TEST=1 to test the native output device")
	}
	b, err := New(Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	devices, supported, err := b.Devices()
	if err != nil || !supported {
		t.Fatalf("native device discovery: supported=%v error=%v", supported, err)
	}
	var selected Device
	for _, device := range devices {
		if device.DefaultOutput && device.Outputs >= 2 {
			selected = device
			break
		}
	}
	if selected.ID == "" {
		t.Fatal("hardware gate requires a default stereo output")
	}
	for _, period := range []int{64, 128, 256, 512, 1024} {
		t.Run(strconv.Itoa(period), func(t *testing.T) {
			cfg := Config{Channels: 2, FramesPerPeriod: period, Device: selected.ID}
			cfg.SampleRate, err = b.SampleRate(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var callbacks atomic.Uint64
			stream, err := b.Open(cfg, func(_ [][]float32, output [][]float32) error {
				for _, channel := range output {
					clear(channel)
				}
				callbacks.Add(1)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if err := stream.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Second)
			stream.Pause()
			f, stats := stream.Format(), stream.Stats()
			if err := stream.Err(); err != nil {
				t.Fatal(err)
			}
			if f.Host != "coreaudio" || f.Device != selected.Name || f.SampleRate != cfg.SampleRate || f.CaptureChannels != 0 || f.FramesPerPeriod < cfg.FramesPerPeriod || f.Latency <= 0 {
				t.Fatalf("selected output or native format lost: %+v", f)
			}
			if callbacks.Load() < 20 || stats.Dropouts != 0 || stats.Callbacks == 0 {
				t.Fatalf("native playback did not stay healthy: callbacks=%d stats=%+v", callbacks.Load(), stats)
			}
			t.Logf("device=%q rate=%d period=%d latency=%s priority=%q callbacks=%d dropouts=%d", f.Device, f.SampleRate, f.FramesPerPeriod, f.Latency, f.Priority, stats.Callbacks, stats.Dropouts)
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
