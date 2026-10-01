package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/internal/audiobackend"
)

type simulatedCaptureStream struct {
	starts, pauses, closes int
	fail                   error
}

func TestCaptureTransportUsesScoreTempoAndKeepsArmedDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.cicada")
	if err := os.WriteFile(path, []byte("tempo 137.333\n"+studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	a, device := simulatedCaptureAudio(256, zeroAudioSource{})
	r, _ := capture.NewRecorder(4, 256, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	tx := &studioTransport{path: path, audio: a, sampleRate: 48000, audioOptions: studioAudioOptions{InputEnabled: true, MonitorMuted: true, MonitorMode: "stereo"}}
	defer tx.close()
	if err := tx.armCapture(r); err != nil {
		t.Fatal(err)
	}
	if err := tx.startCapture(capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	countIn, err := capture.DefaultCountIn(48000, tx.stream.BPMMilli())
	if err != nil || r.Snapshot().CountInFrames != countIn.Frames || tx.stream.BPMMilli() != 137333 {
		t.Fatalf("recording tempo mismatch: %d, %+v, %v", tx.stream.BPMMilli(), r.Snapshot(), err)
	}
	tx.stop()
	if tx.audio != a || tx.stream != nil || device.starts != 1 || device.pauses != 0 || device.closes != 0 {
		t.Fatal("transport Stop lost armed device")
	}
	if err := tx.start(); err != nil {
		t.Fatal(err)
	}
	if device.starts != 1 {
		t.Fatal("transport resume restarted armed device")
	}
	tx.stop()
	if err := tx.disarmCapture(); err != nil {
		t.Fatal(err)
	}
}

func (s *simulatedCaptureStream) Start() error              { s.starts++; return s.fail }
func (s *simulatedCaptureStream) Pause()                    { s.pauses++ }
func (*simulatedCaptureStream) StopClosesDevice() bool      { return true }
func (*simulatedCaptureStream) Err() error                  { return nil }
func (s *simulatedCaptureStream) Close() error              { s.closes++; return nil }
func (*simulatedCaptureStream) Format() audiobackend.Format { return audiobackend.Format{} }
func (*simulatedCaptureStream) Stats() audiobackend.Stats   { return audiobackend.Stats{} }

func simulatedCaptureAudio(frames int, reader io.Reader) (*backendStudioAudio, *simulatedCaptureStream) {
	s := &simulatedCaptureStream{}
	a := &backendStudioAudio{reader: reader, stream: s, pcm: make([]byte, frames*8), engineEpoch: studioEngineEpoch.Add(1), actual: audiobackend.Format{SampleRate: 48000, FramesPerPeriod: frames, Channels: 2, CaptureChannels: 2}}
	return a, s
}

func simulatedCapturePeriod(t *testing.T, a *backendStudioAudio, frame uint64, input, output [][]float32) {
	t.Helper()
	a.captureInput(capture.Block{DeviceEpoch: 1, DeviceFrame: frame, SampleRate: a.actual.SampleRate, Period: a.actual.FramesPerPeriod, Frames: len(output[0]), Layout: capture.LayoutStereo}, input)
	if err := a.renderPeriod(input, output); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureArmPauseStopDisarmLifetime(t *testing.T) {
	a, device := simulatedCaptureAudio(8, zeroAudioSource{})
	r, _ := capture.NewRecorder(4, 8, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	if err := a.ArmCapture(r); err != nil {
		t.Fatal(err)
	}
	input, output := [][]float32{make([]float32, 8), make([]float32, 8)}, [][]float32{make([]float32, 8), make([]float32, 8)}
	input[0][0] = 0.75
	simulatedCapturePeriod(t, a, 0, input, output)
	if device.starts != 1 || a.playing.Load() || a.engineFrame != 0 || a.StopClosesDevice() || a.Snapshot().InputPeakL != 0.75 {
		t.Fatal("armed stopped device did not stay active")
	}
	for range 2 {
		if err := a.Play(); err != nil {
			t.Fatal(err)
		}
		simulatedCapturePeriod(t, a, 8, input, output)
		a.Pause()
	}
	if device.starts != 1 || device.pauses != 0 {
		t.Fatalf("armed device restarted/stopped: %+v", device)
	}
	tx := &studioTransport{audio: a}
	oldEpoch := a.engineEpoch
	tx.stop()
	if tx.audio != a || device.closes != 0 || device.pauses != 0 {
		t.Fatal("Stop released armed device")
	}
	simulatedCapturePeriod(t, a, 16, input, output)
	if a.engineFrame != 0 || a.engineEpoch == oldEpoch {
		t.Fatal("Stop did not reset engine epoch/cursor")
	}
	if err := tx.disarmCapture(); err != nil {
		t.Fatal(err)
	}
	if device.pauses != 1 || device.closes != 1 || tx.audio != nil || a.Armed() {
		t.Fatalf("Disarm retained stopped device: %+v", device)
	}
}

type captureFaultSource struct{}

func (captureFaultSource) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCaptureStudioCallbackAllocations(t *testing.T) {
	for _, test := range []struct {
		name   string
		slots  int
		bars   int
		reader io.Reader
	}{
		{"normal", 1024, 0, zeroAudioSource{}},
		{"count-in", 1024, 1, zeroAudioSource{}},
		{"overrun", 1, 0, zeroAudioSource{}},
		{"fault", 1, 0, captureFaultSource{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _ := simulatedCaptureAudio(256, test.reader)
			entered, release := make(chan struct{}), make(chan struct{})
			first := true
			r, _ := capture.NewRecorder(test.slots, 256, 2, func(capture.RecordedBlock, [][]float32) error {
				if first {
					first = false
					close(entered)
					<-release
				}
				return nil
			})
			if err := a.ArmCapture(r); err != nil {
				t.Fatal(err)
			}
			c, _ := capture.NewCountIn(48000, 120000, test.bars)
			r.Begin(c, capture.Calibration{})
			a.Play()
			in, out := [][]float32{make([]float32, 256), make([]float32, 256)}, [][]float32{make([]float32, 256), make([]float32, 256)}
			b := capture.Block{DeviceEpoch: 1, SampleRate: 48000, Period: 256, Frames: 256, Layout: capture.LayoutStereo}
			a.captureInput(b, in)
			a.renderPeriod(in, out)
			<-entered
			n := testing.AllocsPerRun(1000, func() { a.captureInput(b, in); a.renderPeriod(in, out) })
			close(release)
			a.Close()
			if n != 0 {
				t.Fatalf("callback allocations = %g", n)
			}
		})
	}
}

type countingCaptureSource struct{ frames int }

func (s *countingCaptureSource) Read(b []byte) (int, error) {
	s.frames += len(b) / 8
	clear(b)
	return len(b), nil
}

func TestCaptureCountInSplitsDevicePeriodAndRetainsRaw(t *testing.T) {
	for _, period := range []int{128, 256, 480} {
		source := &countingCaptureSource{}
		a, device := simulatedCaptureAudio(period, source)
		var first, last capture.RecordedBlock
		var rawFirst float32
		r, _ := capture.NewRecorder(1024, period, 2, func(b capture.RecordedBlock, in [][]float32) error {
			if b.RawFrame == 0 {
				first, rawFirst = b, in[0][0]
			}
			last = b
			return nil
		})
		if err := a.ArmCapture(r); err != nil {
			t.Fatal(err)
		}
		c, _ := capture.DefaultCountIn(48000, 137333)
		if err := r.Begin(c, capture.Calibration{}); err != nil {
			t.Fatal(err)
		}
		a.Play()
		in, out := [][]float32{make([]float32, period), make([]float32, period)}, [][]float32{make([]float32, period), make([]float32, period)}
		in[0][0] = 0.75
		var total int64
		for total <= c.Frames {
			simulatedCapturePeriod(t, a, uint64(total), in, out)
			total += int64(period)
			if source.frames != int(max(int64(0), total-c.Frames)) {
				t.Fatalf("period %d: rendered %d frames at %d; count-in %d", period, source.frames, total, c.Frames)
			}
		}
		a.Pause()
		if err := a.DisarmCapture(); err != nil {
			t.Fatal(err)
		}
		if first.Timing.EngineFrame != -c.Frames || rawFirst != 0.75 || last.Timing.EngineFrame != int64(last.Timing.DeviceFrame)-c.Frames || r.Snapshot().WrittenFrames != uint64(total) || device.starts != 1 {
			t.Fatalf("period %d: preroll/timing lost: %+v, %+v, %+v", period, first, last, r.Snapshot())
		}
	}
}

func TestCaptureTapsRawBeforeMonitoring(t *testing.T) {
	a, _ := simulatedCaptureAudio(8, zeroAudioSource{})
	a.SetMonitor(studioAudioMonitor{Gain: 0.5, Mode: "stereo"})
	var raw float32
	r, _ := capture.NewRecorder(4, 8, 2, func(_ capture.RecordedBlock, in [][]float32) error { raw = in[0][0]; return nil })
	a.ArmCapture(r)
	c, _ := capture.NewCountIn(48000, 120000, 0)
	r.Begin(c, capture.Calibration{})
	a.Play()
	in, out := [][]float32{{0.75, 0, 0, 0, 0, 0, 0, 0}, make([]float32, 8)}, [][]float32{make([]float32, 8), make([]float32, 8)}
	simulatedCapturePeriod(t, a, 0, in, out)
	a.Pause()
	a.DisarmCapture()
	if raw != 0.75 || out[0][0] != 0.375 {
		t.Fatalf("raw %g, monitored %g", raw, out[0][0])
	}
}

func TestCaptureArmFailureRollsBackAndDisarmKeepsPlayback(t *testing.T) {
	a, device := simulatedCaptureAudio(8, zeroAudioSource{})
	r, _ := capture.NewRecorder(4, 8, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	defer r.Close()
	device.fail = errors.New("simulated device loss")
	if err := a.ArmCapture(r); err == nil || a.Armed() {
		t.Fatal("failed arm remained active")
	}
	device.fail = nil
	a.Play()
	if err := a.ArmCapture(r); err != nil {
		t.Fatal(err)
	}
	if err := a.DisarmCapture(); err != nil {
		t.Fatal(err)
	}
	if device.pauses != 0 || !a.playing.Load() {
		t.Fatal("disarm stopped playback")
	}
	a.Close()
}
